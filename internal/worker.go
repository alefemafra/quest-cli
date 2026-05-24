package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var transientPatterns = []string{
	"socket connection was closed",
	"connection reset by peer",
	"ECONNRESET",
	"ETIMEDOUT",
	"ECONNREFUSED",
	"network timeout",
	"overloaded_error",
	"rate_limit",
	"529",
	"503",
	"502",
}

const maxTransientRetries = 5

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, p := range transientPatterns {
		if strings.Contains(msg, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

type WorkerStatus string

const (
	WorkerPending             WorkerStatus = "pending"
	WorkerRunning             WorkerStatus = "running"
	WorkerDone                WorkerStatus = "done"
	WorkerFailed              WorkerStatus = "failed"
	WorkerAwaitingValidation  WorkerStatus = "awaiting_validation"
	WorkerValidating          WorkerStatus = "validating"
	WorkerRefining            WorkerStatus = "refining"
)

type FeatureWorker struct {
	Feature        Feature
	Status         WorkerStatus
	Lines          []string
	LastLine       string
	StartTime      time.Time
	EndTime        time.Time
	FailureContext string
	cmd            *exec.Cmd
}

type WorkerPool struct {
	projectDir      string
	missionDir      string
	workers         map[string]*FeatureWorker
	phases          map[int][]string
	logger          *MissionLogger
	eventCh         chan WorkerEvent
	mu              sync.Mutex
	fileMu          sync.Mutex
	stopped         bool
	verbose         *bool
	retries            map[string]int
	maxRetries         int
	transientRetries   map[string]int
	validatorRetries   map[string]int
	maxValidatorRetries int
	refinementCount  map[string]int
	maxRefinements   int
	phaseRetries     map[int]int
	maxPhaseRetries  int
	criticDone       bool
	criticPassed     bool
}

func NewWorkerPool(projectDir, missionDir string, features []Feature, logger *MissionLogger, verbose *bool) *WorkerPool {
	workers := make(map[string]*FeatureWorker)
	phases := make(map[int][]string)

	for _, f := range features {
		workers[f.ID] = &FeatureWorker{
			Feature: f,
			Status:  WorkerPending,
		}
		phases[f.Phase] = append(phases[f.Phase], f.ID)
	}

	return &WorkerPool{
		projectDir:          projectDir,
		missionDir:          missionDir,
		workers:             workers,
		phases:              phases,
		logger:              logger,
		eventCh:             make(chan WorkerEvent, 256),
		verbose:             verbose,
		retries:             make(map[string]int),
		maxRetries:          3,
		transientRetries:    make(map[string]int),
		validatorRetries:    make(map[string]int),
		maxValidatorRetries: 2,
		refinementCount:     make(map[string]int),
		maxRefinements:      3,
		phaseRetries:        make(map[int]int),
		maxPhaseRetries:     1,
	}
}

func (wp *WorkerPool) Start() tea.Cmd {
	wp.logger.Log("", "Mission execution started — %d features", len(wp.workers))

	minPhase := 999
	for phase := range wp.phases {
		if phase < minPhase {
			minPhase = phase
		}
	}

	contract := readFileContent(filepath.Join(wp.missionDir, "validation-contract.md"))

	go func() {
		wp.logger.Log("", "Running critic gate before workers...")

		criticCh := make(chan WorkerEvent, 64)
		go RunCriticGate(wp.projectDir, wp.missionDir, wp.verbose, criticCh)

		var passed bool
		for ev := range criticCh {
			wp.eventCh <- ev
			if ev.Done && ev.Role == "critic" {
				passed = ev.Verdict == "PASS"
				break
			}
		}

		wp.mu.Lock()
		wp.criticDone = true
		wp.criticPassed = passed
		if wp.stopped {
			wp.mu.Unlock()
			return
		}
		wp.mu.Unlock()

		if !passed {
			wp.logger.Log("", "Critic gate failed — workers will not start")
			wp.eventCh <- WorkerEvent{
				AllDone: true,
				Line:    "✕ Critic gate failed — fix issues and retry",
			}
			return
		}

		wp.logger.Log("", "Critic gate passed — starting workers")
		wp.runPhase(minPhase, contract)
	}()

	return listenWorker(wp.eventCh)
}

func (wp *WorkerPool) Stop() {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	wp.stopped = true

	for _, w := range wp.workers {
		if (w.Status == WorkerRunning || w.Status == WorkerAwaitingValidation || w.Status == WorkerValidating || w.Status == WorkerRefining) && w.cmd != nil {
			_ = w.cmd.Process.Kill()
			w.Status = WorkerFailed
			w.EndTime = time.Now()
		}
	}
	wp.logger.Log("", "Execution stopped by user")
}

func (wp *WorkerPool) GetWorkerStatuses() []FeatureWorker {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	var result []FeatureWorker
	for _, w := range wp.workers {
		cp := *w
		cp.cmd = nil
		result = append(result, cp)
	}
	return result
}

func (wp *WorkerPool) freshKnowledge() string {
	return readFileContent(filepath.Join(wp.missionDir, "knowledge-base.md"))
}

func (wp *WorkerPool) runPhase(phase int, contract string) {
	wp.mu.Lock()
	if wp.stopped {
		wp.mu.Unlock()
		return
	}

	featureIDs, ok := wp.phases[phase]
	if !ok {
		wp.mu.Unlock()
		wp.checkAllDone()
		return
	}

	var allPending []Feature
	var siblingNames []string
	for _, id := range featureIDs {
		w := wp.workers[id]
		if w.Status == WorkerPending {
			allPending = append(allPending, w.Feature)
			siblingNames = append(siblingNames, fmt.Sprintf("%s: %s", w.Feature.ID, w.Feature.Title))
		}
	}
	wp.mu.Unlock()

	if len(allPending) == 0 {
		wp.checkAllDone()
		return
	}

	ids := make([]string, len(allPending))
	for i, f := range allPending {
		ids[i] = f.ID
	}
	wp.logger.Log("", "Phase %d starting — %d features: %s", phase, len(allPending), strings.Join(ids, ", "))

	wp.eventCh <- WorkerEvent{
		Phase: phase,
		Line:  fmt.Sprintf("▶ Phase %d started — %d features", phase, len(allPending)),
	}

	ready := wp.startReadyFeatures(allPending, siblingNames, contract)
	if ready == 0 && len(allPending) > 0 {
		wp.logger.Log("", "Phase %d: no features ready (circular deps?)", phase)
	}
}

func (wp *WorkerPool) depsMetLocked(f Feature) bool {
	for _, depID := range f.DependsOn {
		w, ok := wp.workers[depID]
		if !ok {
			continue
		}
		if w.Status == WorkerDone {
			continue
		}
		// Fix features can proceed when their parent is failed — they exist to fix it.
		if f.Fixes == depID && w.Status == WorkerFailed {
			continue
		}
		return false
	}
	return true
}

func (wp *WorkerPool) depsBlockedLocked(f Feature) (bool, []string) {
	var failedDeps []string
	for _, depID := range f.DependsOn {
		w, ok := wp.workers[depID]
		if !ok {
			continue
		}
		if w.Status == WorkerFailed {
			// Fix features are allowed to run even when their parent failed —
			// that's their whole purpose.
			if f.Fixes == depID {
				continue
			}
			failedDeps = append(failedDeps, depID)
		}
	}
	if len(failedDeps) > 0 {
		return true, failedDeps
	}
	return false, nil
}

func (wp *WorkerPool) startReadyFeatures(candidates []Feature, siblingNames []string, contract string) int {
	started := 0
	wp.mu.Lock()

	var cascadeBlocked []Feature
	for _, f := range candidates {
		if wp.stopped {
			break
		}
		w := wp.workers[f.ID]
		if w.Status != WorkerPending {
			continue
		}
		if isBlocked, failedDeps := wp.depsBlockedLocked(f); isBlocked {
			w.Status = WorkerFailed
			w.EndTime = time.Now()
			wp.logger.Log(f.ID, "Blocked — depends on failed: %s", strings.Join(failedDeps, ", "))
			wp.eventCh <- WorkerEvent{
				FeatureID: f.ID,
				Line:      fmt.Sprintf("✕ %s blocked — depends on failed: %s", f.ID, strings.Join(failedDeps, ", ")),
			}
			cascadeBlocked = append(cascadeBlocked, f)
			continue
		}
		if !wp.depsMetLocked(f) {
			wp.logger.Log(f.ID, "Waiting — deps not satisfied: %s", strings.Join(f.DependsOn, ", "))
			continue
		}
		var siblings []string
		for _, s := range siblingNames {
			if !strings.HasPrefix(s, f.ID+":") {
				siblings = append(siblings, s)
			}
		}
		wp.mu.Unlock()
		wp.startWorker(f, siblings, contract)
		wp.mu.Lock()
		started++
	}

	wp.mu.Unlock()

	for _, f := range cascadeBlocked {
		wp.updateFeatureStatus(f.ID, "blocked")
	}

	return started
}

func (wp *WorkerPool) startWorker(feature Feature, siblings []string, contract string) {
	wp.mu.Lock()
	if wp.stopped {
		wp.mu.Unlock()
		return
	}
	w := wp.workers[feature.ID]
	w.Status = WorkerRunning
	w.StartTime = time.Now()
	wp.mu.Unlock()

	wp.logger.Log(feature.ID, "Worker started — %s", feature.Title)
	wp.updateFeatureStatus(feature.ID, "in_progress")

	wp.eventCh <- WorkerEvent{
		FeatureID: feature.ID,
		Line:      fmt.Sprintf("● Started: %s", feature.Title),
	}

	specPath := filepath.Dir(wp.missionDir)
	knowledge := wp.freshKnowledge()
	failureCtx := w.FailureContext
	prompt := BuildWorkerPrompt(feature, siblings, contract, knowledge, specPath, wp.projectDir, failureCtx)
	ch := make(chan ClaudeStreamMsg, 64)
	cmd := StartClaude(prompt, wp.projectDir, wp.verbose, ch)

	wp.mu.Lock()
	w.cmd = cmd
	wp.mu.Unlock()

	go wp.runWorkerLoop(feature, ch, contract)
}

func (wp *WorkerPool) runWorkerLoop(feature Feature, ch chan ClaudeStreamMsg, contract string) {
	for msg := range ch {
		wp.mu.Lock()
		if wp.stopped {
			wp.mu.Unlock()
			return
		}
		w := wp.workers[feature.ID]
		wp.mu.Unlock()

		if msg.Line != "" {
			wp.mu.Lock()
			w.Lines = append(w.Lines, msg.Line)
			w.LastLine = msg.Line
			if len(w.Lines) > 5000 {
				w.Lines = w.Lines[len(w.Lines)-5000:]
			}
			wp.mu.Unlock()

			wp.logger.Log(feature.ID, "%s", msg.Line)
			wp.eventCh <- WorkerEvent{FeatureID: feature.ID, Line: msg.Line}
		}

		if msg.Done {
			wp.mu.Lock()
			w.EndTime = time.Now()
			elapsed := w.EndTime.Sub(w.StartTime).Round(time.Second)

			if msg.Err != nil {
				transient := isTransientError(msg.Err)
				sessionID := msg.SessionID

				if transient {
					wp.transientRetries[feature.ID]++
				} else {
					wp.retries[feature.ID]++
				}

				attempt := wp.retries[feature.ID]
				tAttempt := wp.transientRetries[feature.ID]
				canRetry := !wp.stopped && ((transient && tAttempt <= maxTransientRetries) || (!transient && attempt <= wp.maxRetries))
				wp.mu.Unlock()

				if canRetry {
					label := ""
					retryNum := attempt
					retryMax := wp.maxRetries
					if transient {
						label = " (transient, resuming)"
						retryNum = tAttempt
						retryMax = maxTransientRetries
					} else if sessionID != "" {
						label = " (resuming session)"
					}

					backoff := time.Duration(retryNum) * 3 * time.Second
					if transient {
						backoff = time.Duration(tAttempt) * 5 * time.Second
					}

					wp.logger.Log(feature.ID, "Error: %v — retrying (%d/%d)%s after %s", msg.Err, retryNum, retryMax, label, elapsed)
					wp.eventCh <- WorkerEvent{
						FeatureID: feature.ID,
						Line:      fmt.Sprintf("⚠ %s error, retrying (%d/%d)%s...", feature.ID, retryNum, retryMax, label),
					}

					time.Sleep(backoff)

					wp.mu.Lock()
					w.Status = WorkerRunning
					w.StartTime = time.Now()
					wp.mu.Unlock()

					newCh := make(chan ClaudeStreamMsg, 64)
					var cmd *exec.Cmd
					if sessionID != "" {
						cmd = StartClaude(
							"An error interrupted your work. Continue implementing the feature from where you left off.",
							wp.projectDir, wp.verbose, newCh,
							"--resume", sessionID,
						)
					} else {
						var siblings []string
						specPath := filepath.Dir(wp.missionDir)
						knowledge := wp.freshKnowledge()
						wp.mu.Lock()
						retryCtx := w.FailureContext
						wp.mu.Unlock()
						prompt := BuildWorkerPrompt(feature, siblings, contract, knowledge, specPath, wp.projectDir, retryCtx)
						cmd = StartClaude(prompt, wp.projectDir, wp.verbose, newCh)
					}

					wp.mu.Lock()
					w.cmd = cmd
					wp.mu.Unlock()

					go wp.runWorkerLoop(feature, newCh, contract)
					return
				}

				wp.mu.Lock()
				w.Status = WorkerFailed
				wp.mu.Unlock()

				wp.logger.Log(feature.ID, "FAILED after %d attempts: %v", attempt, msg.Err)
				wp.logger.Log("", "Worker %s FAILED after %d attempts", feature.ID, attempt)
				wp.updateFeatureStatus(feature.ID, "blocked")

				wp.eventCh <- WorkerEvent{
					FeatureID: feature.ID,
					Done:      true,
					Err:       msg.Err,
					Line:      fmt.Sprintf("✕ %s failed after %d attempts", feature.ID, attempt),
				}

				wp.advanceIfPhaseComplete(feature.Phase, contract)
			} else {
				w.Status = WorkerAwaitingValidation
				wp.mu.Unlock()

				wp.logger.Log(feature.ID, "Worker completed in %s — awaiting validation", elapsed)
				wp.updateFeatureStatus(feature.ID, "awaiting_validation")

				wp.eventCh <- WorkerEvent{
					FeatureID: feature.ID,
					Line:      fmt.Sprintf("✓ %s worker done in %s — awaiting validation...", feature.ID, elapsed),
				}

				go wp.runValidator(feature, contract)
			}
			return
		}
	}
}

func (wp *WorkerPool) runValidator(feature Feature, contract string) {
	wp.mu.Lock()
	if wp.stopped {
		wp.mu.Unlock()
		return
	}
	wp.workers[feature.ID].Status = WorkerValidating
	wp.mu.Unlock()

	wp.updateFeatureStatus(feature.ID, "validating")

	specDir := filepath.Dir(wp.missionDir)
	prompt := BuildValidatorPrompt(feature, wp.missionDir, specDir)
	ch := make(chan ClaudeStreamMsg, 64)
	cmd := StartClaude(prompt, wp.projectDir, wp.verbose, ch, "--max-turns", "25")

	wp.mu.Lock()
	wp.workers[feature.ID].cmd = cmd
	wp.mu.Unlock()

	wp.eventCh <- WorkerEvent{
		FeatureID: feature.ID,
		Role:      "validator",
		Line:      fmt.Sprintf("◎ Validating %s...", feature.ID),
	}

	var resultText string
	var tainted bool
	for msg := range ch {
		wp.mu.Lock()
		if wp.stopped {
			wp.mu.Unlock()
			return
		}
		wp.mu.Unlock()

		if msg.Line != "" {
			if !tainted && isWorkerOutputAccess(msg.Line) {
				tainted = true
				wp.logger.Log(feature.ID, "[VALIDATOR] WARNING: accessed worker output — validation may be biased")
				wp.eventCh <- WorkerEvent{
					FeatureID: feature.ID,
					Role:      "validator",
					Line:      "⚠ WARNING: validator accessed worker output — black-box rule violated",
				}
			}
			wp.logger.Log(feature.ID, "[VALIDATOR] %s", msg.Line)
			wp.eventCh <- WorkerEvent{
				FeatureID: feature.ID,
				Role:      "validator",
				Line:      msg.Line,
			}
		}
		if msg.Done {
			if msg.Err != nil {
				if isTransientError(msg.Err) {
					wp.mu.Lock()
					wp.transientRetries[feature.ID+"_val"]++
					tAttempt := wp.transientRetries[feature.ID+"_val"]
					wp.mu.Unlock()
					if tAttempt <= maxTransientRetries {
						backoff := time.Duration(tAttempt) * 5 * time.Second
						wp.logger.Log(feature.ID, "Validator transient error: %v — retrying (%d/%d) in %s", msg.Err, tAttempt, maxTransientRetries, backoff)
						wp.eventCh <- WorkerEvent{
							FeatureID: feature.ID,
							Role:      "validator",
							Line:      fmt.Sprintf("⚠ %s validator socket error, retrying (%d/%d)...", feature.ID, tAttempt, maxTransientRetries),
						}
						time.Sleep(backoff)
						go wp.runValidator(feature, contract)
						return
					}
				}
				if wp.retryValidator(feature, contract, fmt.Sprintf("error: %v", msg.Err)) {
					return
				}
				wp.logger.Log(feature.ID, "Validator error: %v — treating as BLOCKED", msg.Err)
				wp.mu.Lock()
				wp.workers[feature.ID].Status = WorkerFailed
				wp.workers[feature.ID].EndTime = time.Now()
				wp.mu.Unlock()
				wp.updateFeatureStatus(feature.ID, "blocked")
				wp.eventCh <- WorkerEvent{
					FeatureID: feature.ID,
					Role:      "validator",
					Done:      true,
					Verdict:   "BLOCKED",
					Line:      fmt.Sprintf("✕ %s validator error: %v", feature.ID, msg.Err),
				}
				wp.advanceIfPhaseComplete(feature.Phase, contract)
				return
			}
			resultText = msg.Result
		}
	}

	report := ParseValidatorReport(resultText)
	if report != nil {
		if tainted {
			report.Notes = append(report.Notes, "TAINTED: validator accessed worker output — black-box rule violated, results may be biased")
		}
		wp.persistReport(feature.ID, "validator", report)
	}

	if report == nil {
		if wp.retryValidator(feature, contract, "unparseable output") {
			return
		}
		wp.logger.Log(feature.ID, "Validator returned unparseable output — treating as BLOCKED")
		wp.mu.Lock()
		wp.workers[feature.ID].Status = WorkerFailed
		wp.workers[feature.ID].EndTime = time.Now()
		wp.mu.Unlock()
		wp.updateFeatureStatus(feature.ID, "blocked")
		wp.eventCh <- WorkerEvent{
			FeatureID: feature.ID,
			Role:      "validator",
			Done:      true,
			Verdict:   "BLOCKED",
			Line:      fmt.Sprintf("✕ %s validator: unparseable output", feature.ID),
		}
		wp.advanceIfPhaseComplete(feature.Phase, contract)
		return
	}

	switch report.Verdict {
	case "PASS":
		wp.logger.Log(feature.ID, "Validator PASSED")
		wp.mu.Lock()
		wp.workers[feature.ID].Status = WorkerDone
		wp.workers[feature.ID].EndTime = time.Now()
		wp.mu.Unlock()
		wp.updateFeatureStatus(feature.ID, "done")
		wp.eventCh <- WorkerEvent{
			FeatureID: feature.ID,
			Role:      "validator",
			Done:      true,
			Verdict:   "PASS",
			Line:      fmt.Sprintf("✓ %s validated — PASS", feature.ID),
		}
		wp.advanceIfPhaseComplete(feature.Phase, contract)

	case "FAIL":
		wp.logger.Log(feature.ID, "Validator FAILED — starting refinement")
		wp.mu.Lock()
		wp.workers[feature.ID].Status = WorkerRefining
		wp.mu.Unlock()
		wp.updateFeatureStatus(feature.ID, "refining")
		wp.eventCh <- WorkerEvent{
			FeatureID: feature.ID,
			Role:      "validator",
			Done:      true,
			Verdict:   "FAIL",
			Line:      fmt.Sprintf("✕ %s validation FAILED — refining...", feature.ID),
		}
		go wp.runRefinement(feature, *report, contract)

	default:
		wp.logger.Log(feature.ID, "Validator BLOCKED")
		wp.mu.Lock()
		wp.workers[feature.ID].Status = WorkerFailed
		wp.workers[feature.ID].EndTime = time.Now()
		wp.mu.Unlock()
		wp.updateFeatureStatus(feature.ID, "blocked")
		wp.eventCh <- WorkerEvent{
			FeatureID: feature.ID,
			Role:      "validator",
			Done:      true,
			Verdict:   "BLOCKED",
			Line:      fmt.Sprintf("✕ %s validator BLOCKED", feature.ID),
		}
		wp.advanceIfPhaseComplete(feature.Phase, contract)
	}
}

func (wp *WorkerPool) runRefinement(feature Feature, report ValidatorReport, contract string) {
	wp.mu.Lock()
	if wp.stopped {
		wp.mu.Unlock()
		return
	}
	round := wp.refinementCount[feature.ID] + 1
	wp.refinementCount[feature.ID] = round

	if round > wp.maxRefinements {
		wp.workers[feature.ID].Status = WorkerFailed
		wp.workers[feature.ID].EndTime = time.Now()
		wp.mu.Unlock()

		wp.logger.Log(feature.ID, "Refinement limit reached (%d rounds) — escalating", wp.maxRefinements)
		wp.updateFeatureStatus(feature.ID, "blocked")
		wp.eventCh <- WorkerEvent{
			FeatureID: feature.ID,
			Role:      "refinement",
			Done:      true,
			Line:      fmt.Sprintf("✕ %s refinement limit (%d rounds) — needs manual fix", feature.ID, wp.maxRefinements),
		}
		wp.advanceIfPhaseComplete(feature.Phase, contract)
		return
	}
	wp.mu.Unlock()

	wp.logger.Log(feature.ID, "Refinement round %d/%d", round, wp.maxRefinements)
	wp.eventCh <- WorkerEvent{
		FeatureID: feature.ID,
		Role:      "refinement",
		Line:      fmt.Sprintf("⟳ %s refinement round %d/%d", feature.ID, round, wp.maxRefinements),
	}

	specDir := filepath.Dir(wp.missionDir)
	prompt := BuildRefinementPrompt(feature, report, wp.missionDir, specDir)
	ch := make(chan ClaudeStreamMsg, 64)
	cmd := StartClaude(prompt, wp.projectDir, wp.verbose, ch, "--max-turns", "15")

	wp.mu.Lock()
	wp.workers[feature.ID].cmd = cmd
	wp.mu.Unlock()

	var resultText string
	for msg := range ch {
		wp.mu.Lock()
		if wp.stopped {
			wp.mu.Unlock()
			return
		}
		wp.mu.Unlock()

		if msg.Line != "" {
			wp.logger.Log(feature.ID, "[REFINE] %s", msg.Line)
			wp.eventCh <- WorkerEvent{
				FeatureID: feature.ID,
				Role:      "refinement",
				Line:      msg.Line,
			}
		}
		if msg.Done {
			if msg.Err != nil {
				if isTransientError(msg.Err) {
					wp.mu.Lock()
					wp.transientRetries[feature.ID+"_ref"]++
					tAttempt := wp.transientRetries[feature.ID+"_ref"]
					wp.mu.Unlock()
					if tAttempt <= maxTransientRetries {
						backoff := time.Duration(tAttempt) * 5 * time.Second
						wp.logger.Log(feature.ID, "Refinement transient error: %v — retrying (%d/%d) in %s", msg.Err, tAttempt, maxTransientRetries, backoff)
						wp.eventCh <- WorkerEvent{
							FeatureID: feature.ID,
							Role:      "refinement",
							Line:      fmt.Sprintf("⚠ %s refinement socket error, retrying (%d/%d)...", feature.ID, tAttempt, maxTransientRetries),
						}
						time.Sleep(backoff)
						go wp.runRefinement(feature, report, contract)
						return
					}
				}
				wp.logger.Log(feature.ID, "Refinement error: %v", msg.Err)
				wp.mu.Lock()
				wp.workers[feature.ID].Status = WorkerFailed
				wp.workers[feature.ID].EndTime = time.Now()
				wp.mu.Unlock()
				wp.updateFeatureStatus(feature.ID, "blocked")
				wp.eventCh <- WorkerEvent{
					FeatureID: feature.ID,
					Role:      "refinement",
					Done:      true,
					Line:      fmt.Sprintf("✕ %s refinement error: %v", feature.ID, msg.Err),
				}
				wp.advanceIfPhaseComplete(feature.Phase, contract)
				return
			}
			resultText = msg.Result
		}
	}

	fixes := ParseFixFeatures(resultText)
	if len(fixes) == 0 {
		wp.logger.Log(feature.ID, "Refinement produced no fix features — marking blocked")
		wp.mu.Lock()
		wp.workers[feature.ID].Status = WorkerFailed
		wp.workers[feature.ID].EndTime = time.Now()
		wp.mu.Unlock()
		wp.updateFeatureStatus(feature.ID, "blocked")
		wp.eventCh <- WorkerEvent{
			FeatureID: feature.ID,
			Role:      "refinement",
			Done:      true,
			Line:      fmt.Sprintf("✕ %s refinement produced no fixes", feature.ID),
		}
		wp.advanceIfPhaseComplete(feature.Phase, contract)
		return
	}

	if err := AddFixFeatures(wp.missionDir, fixes, feature.ID, &wp.fileMu); err != nil {
		wp.logger.Log(feature.ID, "Failed to write fix features: %v", err)
	}

	wp.mu.Lock()
	wp.workers[feature.ID].Status = WorkerFailed
	wp.workers[feature.ID].EndTime = time.Now()
	for _, fix := range fixes {
		wp.workers[fix.ID] = &FeatureWorker{
			Feature: fix,
			Status:  WorkerPending,
		}
		wp.phases[fix.Phase] = append(wp.phases[fix.Phase], fix.ID)
	}
	wp.mu.Unlock()

	wp.updateFeatureStatus(feature.ID, "blocked")

	fixIDs := make([]string, len(fixes))
	for i, f := range fixes {
		fixIDs[i] = f.ID
	}
	wp.logger.Log(feature.ID, "Generated %d fix features: %s", len(fixes), strings.Join(fixIDs, ", "))
	wp.eventCh <- WorkerEvent{
		FeatureID: feature.ID,
		Role:      "refinement",
		Done:      true,
		Line:      fmt.Sprintf("⟳ %s → %d fix features: %s", feature.ID, len(fixes), strings.Join(fixIDs, ", ")),
	}

	go wp.runFixCriticAndStart(fixes, contract)
}

func (wp *WorkerPool) runFixCriticAndStart(fixes []Feature, contract string) {
	criticCh := make(chan WorkerEvent, 64)
	go RunFixCriticGate(wp.projectDir, wp.missionDir, fixes, wp.verbose, criticCh)

	var passed bool
	for ev := range criticCh {
		wp.eventCh <- ev
		if ev.Done && ev.Role == "critic" {
			passed = ev.Verdict == "PASS"
			break
		}
	}

	if !passed {
		wp.logger.Log("", "Fix critic gate failed — fix features will not start")
		wp.mu.Lock()
		for _, fix := range fixes {
			if w, ok := wp.workers[fix.ID]; ok {
				w.Status = WorkerFailed
				w.EndTime = time.Now()
			}
		}
		wp.mu.Unlock()
		for _, fix := range fixes {
			wp.updateFeatureStatus(fix.ID, "blocked")
		}
		wp.advanceIfPhaseComplete(fixes[0].Phase, contract)
		return
	}

	wp.logger.Log("", "Fix critic gate passed — starting fix workers")
	wp.runPhase(fixes[0].Phase, contract)
}

func (wp *WorkerPool) retryValidator(feature Feature, contract string, reason string) bool {
	wp.mu.Lock()
	wp.validatorRetries[feature.ID]++
	attempt := wp.validatorRetries[feature.ID]
	canRetry := attempt <= wp.maxValidatorRetries && !wp.stopped
	wp.mu.Unlock()

	if !canRetry {
		return false
	}

	wp.logger.Log(feature.ID, "Validator %s — retrying (%d/%d)", reason, attempt, wp.maxValidatorRetries)
	wp.eventCh <- WorkerEvent{
		FeatureID: feature.ID,
		Role:      "validator",
		Line:      fmt.Sprintf("⚠ %s validator %s — retrying (%d/%d)...", feature.ID, reason, attempt, wp.maxValidatorRetries),
	}

	time.Sleep(time.Duration(attempt) * 2 * time.Second)

	go wp.runValidator(feature, contract)
	return true
}

func (wp *WorkerPool) buildFailureContext(w *FeatureWorker) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("PREVIOUS ATTEMPT FAILED (feature %s).\n", w.Feature.ID))

	reportPath := filepath.Join(wp.missionDir, "runs", w.Feature.ID+"-validator.json")
	if data, err := os.ReadFile(reportPath); err == nil {
		var report ValidatorReport
		if json.Unmarshal(data, &report) == nil && report.Verdict != "" {
			sb.WriteString(fmt.Sprintf("Validator verdict: %s\n", report.Verdict))
			for _, a := range report.Assertions {
				if a.Result != "PASS" {
					sb.WriteString(fmt.Sprintf("  - %s: %s — %s\n", a.ID, a.Result, a.Evidence))
				}
			}
			for _, n := range report.Notes {
				sb.WriteString(fmt.Sprintf("  Note: %s\n", n))
			}
		}
	}

	if len(w.Lines) > 0 {
		sb.WriteString("\nKey log lines from failed run:\n")
		start := 0
		if len(w.Lines) > 30 {
			start = len(w.Lines) - 30
		}
		for _, line := range w.Lines[start:] {
			if strings.Contains(line, "error") || strings.Contains(line, "Error") ||
				strings.Contains(line, "denied") || strings.Contains(line, "fail") ||
				strings.Contains(line, "FAIL") || strings.Contains(line, "✕") ||
				strings.Contains(line, "Cannot") || strings.Contains(line, "cannot") ||
				strings.Contains(line, "missing") || strings.Contains(line, "not found") {
				sb.WriteString(fmt.Sprintf("  %s\n", line))
			}
		}
	}

	sb.WriteString("\nFix the issues above in this attempt. Do NOT repeat the same mistakes.")
	return sb.String()
}

func (wp *WorkerPool) retryFailedInPhase(phase int, contract string) bool {
	wp.mu.Lock()
	wp.phaseRetries[phase]++
	attempt := wp.phaseRetries[phase]
	if attempt > wp.maxPhaseRetries || wp.stopped {
		wp.mu.Unlock()
		return false
	}

	ids := wp.phases[phase]
	var toRetry []Feature
	for _, id := range ids {
		w := wp.workers[id]
		if w.Status == WorkerFailed {
			w.FailureContext = wp.buildFailureContext(w)
			w.Status = WorkerPending
			w.StartTime = time.Time{}
			w.EndTime = time.Time{}
			w.Lines = nil
			w.LastLine = ""
			toRetry = append(toRetry, w.Feature)
		}
	}
	wp.mu.Unlock()

	if len(toRetry) == 0 {
		return false
	}

	for _, f := range toRetry {
		wp.updateFeatureStatus(f.ID, "pending")
	}

	retryIDs := make([]string, len(toRetry))
	for i, f := range toRetry {
		retryIDs[i] = f.ID
	}
	wp.logger.Log("", "Phase %d: retrying %d failed features (%d/%d): %s", phase, len(toRetry), attempt, wp.maxPhaseRetries, strings.Join(retryIDs, ", "))
	wp.eventCh <- WorkerEvent{
		Phase: phase,
		Line:  fmt.Sprintf("⟳ Phase %d retry (%d/%d) — %d features: %s", phase, attempt, wp.maxPhaseRetries, len(toRetry), strings.Join(retryIDs, ", ")),
	}

	go wp.runPhase(phase, contract)
	return true
}

func (wp *WorkerPool) advanceIfPhaseComplete(phase int, contract string) {
	wp.mu.Lock()
	if wp.stopped {
		wp.mu.Unlock()
		return
	}

	ids := wp.phases[phase]

	var waitingInPhase []Feature
	var siblingNames []string
	allTerminal := true
	for _, id := range ids {
		w := wp.workers[id]
		switch w.Status {
		case WorkerPending:
			waitingInPhase = append(waitingInPhase, w.Feature)
			siblingNames = append(siblingNames, fmt.Sprintf("%s: %s", w.Feature.ID, w.Feature.Title))
			allTerminal = false
		case WorkerRunning, WorkerAwaitingValidation, WorkerValidating, WorkerRefining:
			allTerminal = false
		}
	}
	wp.mu.Unlock()

	if len(waitingInPhase) > 0 {
		wp.startReadyFeatures(waitingInPhase, siblingNames, contract)
	}

	if !allTerminal {
		return
	}

	hasFailed := false
	wp.mu.Lock()
	for _, id := range wp.phases[phase] {
		if wp.workers[id].Status == WorkerFailed {
			hasFailed = true
			break
		}
	}
	wp.mu.Unlock()

	if hasFailed {
		if wp.retryFailedInPhase(phase, contract) {
			return
		}
	}

	wp.logger.Log("", "Phase %d complete", phase)
	wp.eventCh <- WorkerEvent{
		Phase: phase,
		Line:  fmt.Sprintf("✓ Phase %d complete", phase),
	}

	nextPhase := -1
	wp.mu.Lock()
	for p, pIDs := range wp.phases {
		if p <= phase {
			continue
		}
		for _, id := range pIDs {
			if wp.workers[id].Status == WorkerPending {
				if nextPhase == -1 || p < nextPhase {
					nextPhase = p
				}
				break
			}
		}
	}
	wp.mu.Unlock()

	if nextPhase == -1 {
		wp.checkAllDone()
		return
	}

	go wp.runPhase(nextPhase, contract)
}

func (wp *WorkerPool) checkAllDone() {
	wp.mu.Lock()
	for _, w := range wp.workers {
		if w.Status == WorkerRunning || w.Status == WorkerPending || w.Status == WorkerAwaitingValidation || w.Status == WorkerValidating || w.Status == WorkerRefining {
			wp.mu.Unlock()
			return
		}
	}
	wp.mu.Unlock()

	var done, failed int
	wp.mu.Lock()
	for _, w := range wp.workers {
		switch w.Status {
		case WorkerDone:
			done++
		case WorkerFailed:
			failed++
		}
	}
	wp.mu.Unlock()

	wp.logger.Log("", "All phases complete — %d done, %d failed", done, failed)
	wp.eventCh <- WorkerEvent{
		AllDone: true,
		Line:    fmt.Sprintf("✓ Execution complete — %d done, %d failed", done, failed),
	}
}

func (wp *WorkerPool) persistReport(featureID, role string, data any) {
	runDir := filepath.Join(wp.missionDir, "runs")
	_ = os.MkdirAll(runDir, 0o755)

	reportJSON, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}

	filename := fmt.Sprintf("%s-%s.json", featureID, role)
	_ = os.WriteFile(filepath.Join(runDir, filename), reportJSON, 0o644)
}

func (wp *WorkerPool) updateFeatureStatus(featureID string, status string) {
	wp.fileMu.Lock()
	defer wp.fileMu.Unlock()

	path := filepath.Join(wp.missionDir, "features.json")
	data, err := os.ReadFile(path)
	if err != nil {
		wp.logger.Log(featureID, "WARN: cannot read features.json: %v", err)
		return
	}

	var manifest FeaturesManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		wp.logger.Log(featureID, "WARN: cannot parse features.json: %v", err)
		return
	}

	update := func(features []Feature) {
		for i := range features {
			if features[i].ID == featureID {
				features[i].Status = status
			}
		}
	}
	update(manifest.Features)
	update(manifest.FixFeatures)

	out, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		wp.logger.Log(featureID, "WARN: cannot write features.json: %v", err)
	}
}

func readFileContent(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func isWorkerOutputAccess(line string) bool {
	lower := strings.ToLower(line)
	patterns := []string{"-worker.json", "-worker.md", "runs/", "worker.json"}
	for _, p := range patterns {
		if strings.Contains(lower, p) && strings.Contains(lower, "worker") {
			if strings.Contains(lower, "▸ read") || strings.Contains(lower, "▸ bash") ||
				strings.Contains(lower, "file_path") {
				return true
			}
		}
	}
	return false
}

func listenWorker(ch chan WorkerEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return WorkerEvent{AllDone: true}
		}
		return ev
	}
}
