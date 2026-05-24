package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func GatherProjectContext(projectDir string) string {
	name := detectProjectName(projectDir)
	owner := detectGitOwner(projectDir)
	arch := detectArchitecture(projectDir)
	stack := detectStack(projectDir)
	deps := detectExternalDeps(projectDir)
	hosting := detectHosting(projectDir)
	structure := getProjectStructure(projectDir)
	routes := detectHTTPRoutes(projectDir)
	tests := detectTestFiles(projectDir)
	cliCmds := detectCLICommands(projectDir)
	claudeMd := readFileContent(filepath.Join(projectDir, "CLAUDE.md"))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Project Context Snapshot\nGenerated: %s\n\n", time.Now().UTC().Format(time.RFC3339)))

	sb.WriteString("## Project\n")
	sb.WriteString(fmt.Sprintf("- Name: %s\n", name))
	if owner != "" {
		sb.WriteString(fmt.Sprintf("- Owner: %s\n", owner))
	}
	if arch != "" {
		sb.WriteString(fmt.Sprintf("- Architecture: %s\n", arch))
	}
	sb.WriteString("\n")

	if stack != "" {
		sb.WriteString("## Stack\n")
		sb.WriteString(stack + "\n\n")
	}

	if deps != "" {
		sb.WriteString("## External Dependencies\n")
		sb.WriteString(deps + "\n\n")
	}

	if hosting != "" {
		sb.WriteString("## Hosting\n")
		sb.WriteString(hosting + "\n\n")
	}

	if structure != "" {
		sb.WriteString("## Project Structure\n")
		sb.WriteString(structure + "\n\n")
	}

	if routes != "" {
		sb.WriteString("## HTTP Routes\n")
		sb.WriteString(routes + "\n\n")
	}

	if tests != "" {
		sb.WriteString("## Test Coverage\n")
		sb.WriteString(tests + "\n\n")
	}

	if cliCmds != "" {
		sb.WriteString("## CLI Commands\n")
		sb.WriteString(cliCmds + "\n\n")
	}

	if claudeMd != "" {
		sb.WriteString("## CLAUDE.md\n\n")
		sb.WriteString(claudeMd + "\n")
	} else {
		sb.WriteString("## CLAUDE.md\nNot found — consider creating with init-mission.\n")
	}

	return sb.String()
}

func detectProjectName(projectDir string) string {
	if name := readJSONStringField(filepath.Join(projectDir, "package.json"), "name"); name != "" {
		return name
	}

	if content := readFileContent(filepath.Join(projectDir, "go.mod")); content != "" {
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(line, "module ") {
				mod := strings.TrimPrefix(line, "module ")
				mod = strings.TrimSpace(mod)
				parts := strings.Split(mod, "/")
				return parts[len(parts)-1]
			}
		}
	}

	if content := readFileContent(filepath.Join(projectDir, "pyproject.toml")); content != "" {
		re := regexp.MustCompile(`(?m)^name\s*=\s*"([^"]+)"`)
		if m := re.FindStringSubmatch(content); len(m) > 1 {
			return m[1]
		}
	}

	if content := readFileContent(filepath.Join(projectDir, "Cargo.toml")); content != "" {
		re := regexp.MustCompile(`(?m)^name\s*=\s*"([^"]+)"`)
		if m := re.FindStringSubmatch(content); len(m) > 1 {
			return m[1]
		}
	}

	return filepath.Base(projectDir)
}

func detectStack(projectDir string) string {
	var items []string

	if data := readFileContent(filepath.Join(projectDir, "package.json")); data != "" {
		items = append(items, detectNodeStack(data)...)
	}

	if content := readFileContent(filepath.Join(projectDir, "go.mod")); content != "" {
		items = append(items, "Go")
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.Contains(trimmed, "github.com/gin-gonic/gin"):
				items = append(items, "Gin")
			case strings.Contains(trimmed, "github.com/gofiber/fiber"):
				items = append(items, "Fiber")
			case strings.Contains(trimmed, "github.com/labstack/echo"):
				items = append(items, "Echo")
			case strings.Contains(trimmed, "github.com/charmbracelet/bubbletea"):
				items = append(items, "Bubbletea")
			case strings.Contains(trimmed, "gorm.io/gorm"):
				items = append(items, "GORM")
			}
		}
	}

	if fileExists(filepath.Join(projectDir, "pyproject.toml")) || fileExists(filepath.Join(projectDir, "requirements.txt")) {
		items = append(items, "Python")
		if content := readFileContent(filepath.Join(projectDir, "pyproject.toml")); content != "" {
			if strings.Contains(content, "fastapi") {
				items = append(items, "FastAPI")
			}
			if strings.Contains(content, "django") {
				items = append(items, "Django")
			}
			if strings.Contains(content, "flask") {
				items = append(items, "Flask")
			}
		}
	}

	if fileExists(filepath.Join(projectDir, "Cargo.toml")) {
		items = append(items, "Rust")
	}

	if fileExists(filepath.Join(projectDir, "Gemfile")) {
		items = append(items, "Ruby")
		if content := readFileContent(filepath.Join(projectDir, "Gemfile")); strings.Contains(content, "rails") {
			items = append(items, "Rails")
		}
	}

	if len(items) == 0 {
		return ""
	}
	return strings.Join(items, ", ")
}

func detectNodeStack(packageJSON string) []string {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(packageJSON), &pkg); err != nil {
		return []string{"Node.js"}
	}

	allDeps := make(map[string]bool)
	for k := range pkg.Dependencies {
		allDeps[k] = true
	}
	for k := range pkg.DevDependencies {
		allDeps[k] = true
	}

	var items []string
	if allDeps["typescript"] {
		items = append(items, "TypeScript")
	} else {
		items = append(items, "JavaScript")
	}

	frameworks := []struct {
		pkg  string
		name string
	}{
		{"next", "Next.js"},
		{"react", "React"},
		{"vue", "Vue"},
		{"svelte", "Svelte"},
		{"@nestjs/core", "NestJS"},
		{"express", "Express"},
		{"fastify", "Fastify"},
		{"@prisma/client", "Prisma"},
		{"drizzle-orm", "Drizzle"},
		{"typeorm", "TypeORM"},
		{"sequelize", "Sequelize"},
		{"tailwindcss", "Tailwind CSS"},
	}

	for _, fw := range frameworks {
		if allDeps[fw.pkg] {
			items = append(items, fw.name)
		}
	}

	return items
}

func detectGitOwner(projectDir string) string {
	cmd := exec.Command("git", "config", "user.name")
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func detectArchitecture(projectDir string) string {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}

	dirs := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs[e.Name()] = true
		}
	}

	switch {
	case dirs["apps"] && dirs["packages"]:
		return "Monorepo"
	case dirs["services"]:
		return "Microservices"
	case dirs["cmd"] && (dirs["pkg"] || dirs["internal"]):
		return "Go binary/service"
	case dirs["src"] && !dirs["apps"]:
		if dirs["frontend"] || dirs["client"] {
			return "Full-stack monolith"
		}
		return "Monolith"
	case dirs["frontend"] && dirs["backend"]:
		return "Full-stack (separated)"
	case dirs["lib"] && !dirs["src"]:
		return "Library"
	}
	return ""
}

func detectExternalDeps(projectDir string) string {
	type depMapping struct {
		packages []string
		category string
	}

	mappings := []depMapping{
		{[]string{"pg", "mysql2", "mongoose", "mongodb", "@prisma/client", "prisma", "sqlite3", "better-sqlite3", "drizzle-orm", "typeorm", "sequelize", "psycopg2", "asyncpg", "pymongo", "sqlalchemy", "sqlx", "diesel", "lib/pq", "pgx", "gorm.io"}, "Database"},
		{[]string{"redis", "ioredis", "memcached", "go-redis"}, "Cache"},
		{[]string{"bullmq", "amqplib", "kafkajs", "sqs-consumer", "celery", "pika"}, "Queue"},
		{[]string{"openai", "@anthropic-ai/sdk", "anthropic", "@google/generative-ai", "cohere-ai", "mistralai"}, "LLM"},
		{[]string{"stripe", "@stripe/stripe-js", "braintree"}, "Payments"},
		{[]string{"nodemailer", "@sendgrid/mail", "resend", "postmark"}, "Email"},
		{[]string{"twilio", "@slack/web-api", "grammy", "discord.js"}, "Messaging"},
		{[]string{"@aws-sdk", "aws-sdk", "@azure", "@google-cloud", "minio", "boto3"}, "Cloud/Storage"},
		{[]string{"next-auth", "auth0", "passport", "@clerk", "@supabase/supabase-js"}, "Auth"},
		{[]string{"elasticsearch", "@elastic/elasticsearch", "meilisearch", "pinecone", "weaviate", "qdrant"}, "Search/Vector"},
		{[]string{"@sentry", "sentry", "pino", "winston", "datadog"}, "Observability"},
	}

	var allDeps []string

	if data := readFileContent(filepath.Join(projectDir, "package.json")); data != "" {
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal([]byte(data), &pkg) == nil {
			for k := range pkg.Dependencies {
				allDeps = append(allDeps, k)
			}
		}
	}

	if content := readFileContent(filepath.Join(projectDir, "go.mod")); content != "" {
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "module") && !strings.HasPrefix(trimmed, "go ") && !strings.HasPrefix(trimmed, "require") && !strings.HasPrefix(trimmed, ")") && !strings.HasPrefix(trimmed, "(") {
				parts := strings.Fields(trimmed)
				if len(parts) > 0 {
					allDeps = append(allDeps, parts[0])
				}
			}
		}
	}

	if content := readFileContent(filepath.Join(projectDir, "requirements.txt")); content != "" {
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				parts := strings.FieldsFunc(line, func(r rune) bool {
					return r == '=' || r == '>' || r == '<' || r == '~' || r == '!'
				})
				if len(parts) > 0 {
					allDeps = append(allDeps, parts[0])
				}
			}
		}
	}

	if len(allDeps) == 0 {
		return ""
	}

	found := make(map[string][]string)
	for _, dep := range allDeps {
		depLower := strings.ToLower(dep)
		for _, m := range mappings {
			for _, pkg := range m.packages {
				if strings.Contains(depLower, strings.ToLower(pkg)) {
					if !containsStr(found[m.category], dep) {
						found[m.category] = append(found[m.category], dep)
					}
					break
				}
			}
		}
	}

	if len(found) == 0 {
		return ""
	}

	var lines []string
	order := []string{"Database", "Cache", "Queue", "LLM", "Payments", "Email", "Messaging", "Cloud/Storage", "Auth", "Search/Vector", "Observability"}
	for _, cat := range order {
		if pkgs, ok := found[cat]; ok {
			lines = append(lines, fmt.Sprintf("- %s: %s", cat, strings.Join(pkgs, ", ")))
		}
	}
	return strings.Join(lines, "\n")
}

func detectHosting(projectDir string) string {
	var found []string

	checks := []struct {
		path string
		name string
	}{
		{"Dockerfile", "Docker"},
		{"docker-compose.yml", "Docker Compose"},
		{"docker-compose.yaml", "Docker Compose"},
		{"vercel.json", "Vercel"},
		{"fly.toml", "Fly.io"},
		{"railway.toml", "Railway"},
		{"render.yaml", "Render"},
		{"netlify.toml", "Netlify"},
		{"app.yaml", "Google App Engine"},
		{"serverless.yml", "Serverless Framework"},
	}

	for _, c := range checks {
		if fileExists(filepath.Join(projectDir, c.path)) {
			found = append(found, c.name)
		}
	}

	if matches, _ := filepath.Glob(filepath.Join(projectDir, ".github", "workflows", "deploy*")); len(matches) > 0 {
		found = append(found, "GitHub Actions (deploy)")
	}

	if len(found) == 0 {
		return ""
	}
	return strings.Join(found, ", ")
}

func getProjectStructure(projectDir string) string {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}

	exclude := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		".next": true, "dist": true, "build": true,
		"__pycache__": true, "target": true, ".cache": true,
	}

	var dirs, files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && e.Name() != ".github" {
			continue
		}
		if exclude[e.Name()] {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e.Name()+"/")
		} else {
			files = append(files, e.Name())
		}
	}

	var parts []string
	if len(dirs) > 0 {
		parts = append(parts, "Directories: "+strings.Join(dirs, " "))
	}

	keyFiles := filterKeyFiles(files)
	if len(keyFiles) > 0 {
		parts = append(parts, "Key files: "+strings.Join(keyFiles, " "))
	}

	return strings.Join(parts, "\n")
}

func filterKeyFiles(files []string) []string {
	key := map[string]bool{
		"package.json": true, "go.mod": true, "pyproject.toml": true,
		"Cargo.toml": true, "Gemfile": true, "composer.json": true,
		"Makefile": true, "Dockerfile": true, "docker-compose.yml": true,
		"tsconfig.json": true, "CLAUDE.md": true, "README.md": true,
		".env.example": true, "prisma": true,
	}

	var result []string
	for _, f := range files {
		if key[f] {
			result = append(result, f)
		}
	}
	return result
}

func readJSONStringField(path, field string) string {
	data := readFileContent(path)
	if data == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return ""
	}
	if v, ok := obj[field].(string); ok {
		return v
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// GatherSpecCodeContext pre-reads source files relevant to a spec's domain
// so they can be embedded in the prompt, eliminating the need for Claude to explore.
func GatherSpecCodeContext(specDir, projectDir string) string {
	slug := filepath.Base(specDir)
	parent := filepath.Base(filepath.Dir(specDir))

	// Extract domain keywords from slug path (e.g. "domain/events/list" → ["events", "event"])
	keywords := extractDomainKeywords(slug, parent)
	if len(keywords) == 0 {
		return ""
	}

	var files []codeFile
	totalSize := 0
	const maxTotal = 150_000 // ~150KB of source context

	// 1. Find all source files, filter by domain keywords in Go
	srcDir := filepath.Join(projectDir, "src")
	if !fileExists(srcDir) {
		srcDir = projectDir
	}
	cmd := exec.Command("find", srcDir,
		"-type", "f",
		"-not", "-path", "*/node_modules/*",
		"-not", "-path", "*/.git/*",
		"-not", "-path", "*/dist/*",
		"-not", "-path", "*/.next/*",
		"-not", "-path", "*/build/*",
	)
	allFiles, _ := cmd.Output()

	seen := make(map[string]bool)
	for _, f := range strings.Split(strings.TrimSpace(string(allFiles)), "\n") {
		if f == "" {
			continue
		}
		ext := filepath.Ext(f)
		if ext != ".ts" && ext != ".tsx" && ext != ".js" && ext != ".jsx" && ext != ".json" && ext != ".go" && ext != ".py" {
			continue
		}
		pathLower := strings.ToLower(f)
		matched := false
		for _, kw := range keywords {
			kwLower := strings.ToLower(kw)
			if strings.Contains(pathLower, kwLower) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		rel, _ := filepath.Rel(projectDir, f)
		if rel == "" || seen[rel] {
			continue
		}
		content := readFileContent(f)
		if content == "" || len(content) > 20_000 {
			continue
		}
		if totalSize+len(content) > maxTotal {
			break
		}
		seen[rel] = true
		files = append(files, codeFile{Path: rel, Content: content})
		totalSize += len(content)
	}

	// 2. Add key structural files (barrel exports, route files)
	structuralPatterns := []string{
		"src/modules/*/index.ts",
		"src/modules/core/index.ts",
		"src/app/routes/*" + keywords[0] + "*",
	}
	for _, pat := range structuralPatterns {
		matches, _ := filepath.Glob(filepath.Join(projectDir, pat))
		for _, f := range matches {
			rel, _ := filepath.Rel(projectDir, f)
			if rel == "" || seen[rel] {
				continue
			}
			content := readFileContent(f)
			if content == "" || len(content) > 20_000 {
				continue
			}
			if totalSize+len(content) > maxTotal {
				break
			}
			seen[rel] = true
			files = append(files, codeFile{Path: rel, Content: content})
			totalSize += len(content)
		}
	}

	if len(files) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Pre-read %d source files matching domain '%s' (%d bytes):\n\n", len(files), keywords[0], totalSize))
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("### %s\n\n```\n%s\n```\n\n", f.Path, f.Content))
	}
	return sb.String()
}

type codeFile struct {
	Path    string
	Content string
}

func extractDomainKeywords(slug, parent string) []string {
	// slug examples: "list", "create", "edit"
	// parent examples: "events", "venues", "orders"
	// full path: "domain/events/list" → parent=events, slug=list
	var keywords []string

	if parent != "" && parent != "domain" && parent != "specs" && parent != "docs" {
		keywords = append(keywords, parent)
		// Add singular form if plural
		if strings.HasSuffix(parent, "s") && len(parent) > 3 {
			keywords = append(keywords, parent[:len(parent)-1])
		}
	}

	// Also try the slug itself if it's descriptive
	if slug != "list" && slug != "create" && slug != "edit" && slug != "delete" && slug != "detail" && slug != "view" {
		keywords = append(keywords, slug)
	}

	return keywords
}


func detectHTTPRoutes(projectDir string) string {
	type routePattern struct {
		extensions []string
		pattern    string
		label      string
	}

	patterns := []routePattern{
		{[]string{"*.ts", "*.js", "*.mjs"}, `(app|router|server)\.(get|post|put|patch|delete|all)\s*\(`, "Express/Fastify"},
		{[]string{"*.ts", "*.js"}, `@(Get|Post|Put|Patch|Delete|All)\s*\(`, "NestJS"},
		{[]string{"*.go"}, `(HandleFunc|Handle|\.GET|\.POST|\.PUT|\.PATCH|\.DELETE)\(`, "Go"},
		{[]string{"*.py"}, `@(app|router|bp)\.(get|post|put|patch|delete|route)\s*\(`, "FastAPI/Flask"},
		{[]string{"*.py"}, `path\s*\(\s*['"]`, "Django"},
		{[]string{"*.rb"}, `(get|post|put|patch|delete|resources?)\s+['":].+`, "Rails"},
	}

	seen := make(map[string]bool)
	var results []string

	for _, p := range patterns {
		for _, ext := range p.extensions {
			cmd := exec.Command("grep", "-r", "-l",
				"--include="+ext,
				"--exclude-dir=node_modules",
				"--exclude-dir=vendor",
				"--exclude-dir=.git",
				"--exclude-dir=dist",
				"--exclude-dir=build",
				"--exclude-dir=.next",
				"-E", p.pattern,
				projectDir,
			)
			out, err := cmd.Output()
			if err != nil || len(out) == 0 {
				continue
			}
			for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				rel, _ := filepath.Rel(projectDir, f)
				if rel == "" {
					continue
				}
				entry := fmt.Sprintf("- %s (%s)", rel, p.label)
				if !seen[entry] {
					seen[entry] = true
					results = append(results, entry)
				}
			}
		}
	}

	if len(results) == 0 {
		return ""
	}
	if len(results) > 20 {
		results = append(results[:20], fmt.Sprintf("- ... and %d more files", len(results)-20))
	}
	return strings.Join(results, "\n")
}

func detectTestFiles(projectDir string) string {
	testPatterns := []struct {
		pattern string
		label   string
	}{
		{"*_test.go", "Go"},
		{"*.test.ts", "TypeScript"},
		{"*.test.tsx", "TypeScript/React"},
		{"*.spec.ts", "TypeScript"},
		{"*.spec.tsx", "TypeScript/React"},
		{"*.test.js", "JavaScript"},
		{"*.test.jsx", "JavaScript/React"},
		{"*.spec.js", "JavaScript"},
		{"test_*.py", "Python"},
		{"*_test.py", "Python"},
		{"*_spec.rb", "Ruby"},
	}

	var lines []string
	total := 0
	for _, tp := range testPatterns {
		cmd := exec.Command("find", projectDir,
			"-name", tp.pattern,
			"-not", "-path", "*/node_modules/*",
			"-not", "-path", "*/vendor/*",
			"-not", "-path", "*/.git/*",
		)
		out, err := cmd.Output()
		if err != nil || len(strings.TrimSpace(string(out))) == 0 {
			continue
		}
		count := len(strings.Split(strings.TrimSpace(string(out)), "\n"))
		total += count
		lines = append(lines, fmt.Sprintf("- %s (%s): %d files", tp.pattern, tp.label, count))
	}

	if total == 0 {
		return ""
	}
	lines = append(lines, fmt.Sprintf("- Total: %d test files", total))
	return strings.Join(lines, "\n")
}

func detectCLICommands(projectDir string) string {
	type cliPattern struct {
		extensions []string
		pattern    string
		label      string
	}

	patterns := []cliPattern{
		{[]string{"*.ts", "*.js", "*.mjs"}, `\.command\s*\(|program\.(parse|version|option)`, "Commander.js"},
		{[]string{"*.ts", "*.js", "*.mjs"}, `yargs\.|\.argv|\.positional\(`, "yargs"},
		{[]string{"*.go"}, `cobra\.Command\{|AddCommand\(|rootCmd`, "cobra"},
		{[]string{"*.py"}, `argparse\.ArgumentParser|@click\.(command|group)|typer\.(Typer|Argument|Option)`, "Python CLI"},
	}

	seen := make(map[string]bool)
	var results []string

	for _, p := range patterns {
		for _, ext := range p.extensions {
			cmd := exec.Command("grep", "-r", "-l",
				"--include="+ext,
				"--exclude-dir=node_modules",
				"--exclude-dir=vendor",
				"--exclude-dir=.git",
				"--exclude-dir=dist",
				"--exclude-dir=build",
				"--exclude-dir=.next",
				"-E", p.pattern,
				projectDir,
			)
			out, err := cmd.Output()
			if err != nil || len(out) == 0 {
				continue
			}
			for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				rel, _ := filepath.Rel(projectDir, f)
				if rel == "" {
					continue
				}
				entry := fmt.Sprintf("- %s (%s)", rel, p.label)
				if !seen[entry] {
					seen[entry] = true
					results = append(results, entry)
				}
			}
		}
	}

	if len(results) == 0 {
		return ""
	}
	return strings.Join(results, "\n")
}
