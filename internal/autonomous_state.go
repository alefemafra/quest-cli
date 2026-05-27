package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const autonomousStateFileName = "autonomous-state.json"

// AutonomousRuntimeState captures runtime-only orchestration metadata that
// allows the worker pool to continue autonomous recovery after process restart.
type AutonomousRuntimeState struct {
	LastSessionIDs         map[string]string `json:"lastSessionIds"`
	FailureSignatures      map[string]int    `json:"failureSignatures"`
	RecoveryLevel          map[string]int    `json:"recoveryLevel"`
	CriticTransientAttempt map[string]int    `json:"criticTransientAttempts"`
	CriticStructuralTry    map[string]int    `json:"criticStructuralAttempts"`
	CriticAutoFixAttempt   map[string]int    `json:"criticAutoFixAttempts"`
	CriticBypassCount      map[string]int    `json:"criticBypassCount"`
	AutoResetCount         int               `json:"autoResetCount"`
	AutoRegenCount         int               `json:"autoRegenCount"`
	UpdatedAt              string            `json:"updatedAt,omitempty"`
}

var autonomousStateFileMu sync.Mutex

func newAutonomousRuntimeState() AutonomousRuntimeState {
	return AutonomousRuntimeState{
		LastSessionIDs:         make(map[string]string),
		FailureSignatures:      make(map[string]int),
		RecoveryLevel:          make(map[string]int),
		CriticTransientAttempt: make(map[string]int),
		CriticStructuralTry:    make(map[string]int),
		CriticAutoFixAttempt:   make(map[string]int),
		CriticBypassCount:      make(map[string]int),
	}
}

func ensureAutonomousState(state *AutonomousRuntimeState) {
	if state.LastSessionIDs == nil {
		state.LastSessionIDs = make(map[string]string)
	}
	if state.FailureSignatures == nil {
		state.FailureSignatures = make(map[string]int)
	}
	if state.RecoveryLevel == nil {
		state.RecoveryLevel = make(map[string]int)
	}
	if state.CriticTransientAttempt == nil {
		state.CriticTransientAttempt = make(map[string]int)
	}
	if state.CriticStructuralTry == nil {
		state.CriticStructuralTry = make(map[string]int)
	}
	if state.CriticAutoFixAttempt == nil {
		state.CriticAutoFixAttempt = make(map[string]int)
	}
	if state.CriticBypassCount == nil {
		state.CriticBypassCount = make(map[string]int)
	}
}

func autonomousStatePath(missionDir string) string {
	return filepath.Join(missionDir, "runs", autonomousStateFileName)
}

func loadAutonomousRuntimeState(missionDir string) AutonomousRuntimeState {
	autonomousStateFileMu.Lock()
	defer autonomousStateFileMu.Unlock()
	return loadAutonomousRuntimeStateLocked(missionDir)
}

func loadAutonomousRuntimeStateLocked(missionDir string) AutonomousRuntimeState {
	state := newAutonomousRuntimeState()
	if missionDir == "" {
		return state
	}

	data, err := os.ReadFile(autonomousStatePath(missionDir))
	if err != nil {
		return state
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return newAutonomousRuntimeState()
	}
	ensureAutonomousState(&state)
	return state
}

func saveAutonomousRuntimeState(missionDir string, state AutonomousRuntimeState) error {
	autonomousStateFileMu.Lock()
	defer autonomousStateFileMu.Unlock()
	return saveAutonomousRuntimeStateLocked(missionDir, state)
}

func saveAutonomousRuntimeStateLocked(missionDir string, state AutonomousRuntimeState) error {
	if missionDir == "" {
		return nil
	}
	ensureAutonomousState(&state)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	runsDir := filepath.Join(missionDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(autonomousStatePath(missionDir), data, 0o644)
}

func updateAutonomousRuntimeState(missionDir string, mutate func(state *AutonomousRuntimeState)) (AutonomousRuntimeState, error) {
	autonomousStateFileMu.Lock()
	defer autonomousStateFileMu.Unlock()

	state := loadAutonomousRuntimeStateLocked(missionDir)
	mutate(&state)
	ensureAutonomousState(&state)
	if err := saveAutonomousRuntimeStateLocked(missionDir, state); err != nil {
		return state, err
	}
	return state, nil
}

func autonomousSessionKey(role, featureID string) string {
	if featureID == "" {
		return role
	}
	return role + ":" + featureID
}
