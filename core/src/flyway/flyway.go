/*
 * Copyright 2026 Clidey, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package flyway

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/clidey/whodb/core/src/common/datadir"
	"github.com/clidey/whodb/core/src/engine"
	"github.com/clidey/whodb/core/src/opsplane"
	"github.com/clidey/whodb/core/src/source"
)

const defaultFlywayVersion = "11.3.4"

// Status describes the local Flyway CLI installation status.
type Status struct {
	Installed     bool
	Version       string
	BinaryPath    string
	InstallMethod string
}

// InstallResult holds the outcome of installing the Flyway CLI.
type InstallResult struct {
	Success bool
	Status  Status
	Output  string
	Error   string
}

// MigrationFile represents a discovered SQL migration file in a directory.
type MigrationFile struct {
	Filename     string
	RelativePath string
	Version      string
	Description  string
	Type         string
	SizeBytes    int64
}

// DirectoryPreview describes the migration files found inside a user-selected directory.
type DirectoryPreview struct {
	Directory string
	Exists    bool
	Files     []MigrationFile
	Error     string
}

// ExecuteRequest configures a Flyway command execution against a target database.
type ExecuteRequest struct {
	Credentials       *source.Credentials
	TargetDatabase    string
	TargetSchema      string
	Directory         string
	Action            string
	BaselineOnMigrate bool
	OutOfOrder        bool
	ValidateOnMigrate bool
	// RecoveryPlanner supplies the selected connector's read-only DDL verifier.
	RecoveryPlanner func(context.Context, string) (*engine.MigrationRecoveryPlan, error) `json:"-"`
	// StructureRecoveryConfirmed authorizes only a verified connector recovery plan.
	StructureRecoveryConfirmed bool `json:"-"`
	outputJSON                 bool
	skipCallbacks              bool
	reviewDirectory            string
	migrationTarget            string
}

// ExecuteResult holds the output and exit status of a Flyway command execution.
type ExecuteResult struct {
	Success    bool
	Action     string
	Command    string
	Output     string
	Error      string
	DurationMs int
}

var (
	versionedPattern  = regexp.MustCompile(`^V([0-9A-Za-z._-]+)__(.+)\.sql$`)
	undoPattern       = regexp.MustCompile(`^U([0-9A-Za-z._-]+)__(.+)\.sql$`)
	repeatablePattern = regexp.MustCompile(`^R__(.+)\.sql$`)
	flywayVerPattern  = regexp.MustCompile(`(?i)Flyway\s+(?:Community|Teams|Enterprise)?\s*Edition\s+([0-9][0-9A-Za-z._-]*)`)
)

func managedToolsDir() (string, error) {
	base, err := datadir.Get(datadir.Options{AppName: "whodb"})
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "tools", "flyway")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func candidateBinaryPaths() []string {
	var candidates []string
	if dir, err := managedToolsDir(); err == nil {
		binName := "flyway"
		if runtime.GOOS == "windows" {
			binName = "flyway.cmd"
		}
		candidates = append(candidates, filepath.Join(dir, binName))
		if entries, err := os.ReadDir(dir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && strings.HasPrefix(entry.Name(), "flyway-") {
					candidates = append(candidates, filepath.Join(dir, entry.Name(), binName))
				}
			}
		}
	}
	if runtime.GOOS != "windows" {
		candidates = append(candidates,
			"/opt/homebrew/bin/flyway",
			"/usr/local/bin/flyway",
		)
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".flyway", "flyway"))
		}
	}
	if lookedUp, err := exec.LookPath("flyway"); err == nil && lookedUp != "" {
		candidates = append(candidates, lookedUp)
	}
	return candidates
}

func buildCommandEnv() []string {
	env := os.Environ()
	extraPaths := []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"}
	if runtime.GOOS == "darwin" {
		extraPaths = append([]string{
			"/opt/homebrew/opt/openjdk/bin",
			"/usr/local/opt/openjdk/bin",
		}, extraPaths...)
	}
	pathUpdated := false
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			currentPath := strings.TrimPrefix(kv, "PATH=")
			env[i] = "PATH=" + strings.Join(extraPaths, string(os.PathListSeparator)) + string(os.PathListSeparator) + currentPath
			pathUpdated = true
			break
		}
	}
	if !pathUpdated {
		env = append(env, "PATH="+strings.Join(extraPaths, string(os.PathListSeparator)))
	}
	return env
}

// GetStatus detects whether Flyway CLI is installed and returns its version and binary path.
func GetStatus(ctx context.Context) Status {
	if ctx == nil {
		ctx = context.Background()
	}
	seen := map[string]bool{}
	for _, candidate := range candidateBinaryPaths() {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		verCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		cmd := exec.CommandContext(verCtx, candidate, "-v")
		cmd.Env = buildCommandEnv()
		out, err := cmd.CombinedOutput()
		cancel()
		outputStr := strings.TrimSpace(string(out))
		if err != nil && !strings.Contains(strings.ToLower(outputStr), "flyway") {
			continue
		}
		version := parseFlywayVersion(outputStr)
		method := "system"
		if strings.Contains(candidate, "/homebrew/") || strings.Contains(candidate, "/usr/local/") {
			method = "homebrew"
		} else if dir, dirErr := managedToolsDir(); dirErr == nil && strings.HasPrefix(candidate, dir) {
			method = "managed"
		}
		return Status{
			Installed:     true,
			Version:       version,
			BinaryPath:    candidate,
			InstallMethod: method,
		}
	}
	return Status{Installed: false}
}

func parseFlywayVersion(output string) string {
	if m := flywayVerPattern.FindStringSubmatch(output); len(m) >= 2 {
		return m[1]
	}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "flyway") {
			return trimmed
		}
	}
	if output != "" {
		firstLine := strings.TrimSpace(strings.Split(output, "\n")[0])
		if len(firstLine) > 80 {
			return firstLine[:80]
		}
		return firstLine
	}
	return "installed"
}

// Install installs the Flyway CLI via Homebrew (when available on macOS/Linux) or direct Redgate archive download.
func Install(ctx context.Context) InstallResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing := GetStatus(ctx); existing.Installed {
		return InstallResult{
			Success: true,
			Status:  existing,
			Output:  fmt.Sprintf("Flyway CLI is already installed at %s (%s)", existing.BinaryPath, existing.Version),
		}
	}

	var logs []string

	// 1. Try Homebrew on macOS / Linux if available
	brewPath := findBrewBinary()
	if brewPath != "" {
		logs = append(logs, fmt.Sprintf("Using Homebrew (%s) to install Flyway CLI...", brewPath))
		brewCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		cmd := exec.CommandContext(brewCtx, brewPath, "install", "flyway")
		cmd.Env = buildCommandEnv()
		out, err := cmd.CombinedOutput()
		cancel()
		if len(out) > 0 {
			logs = append(logs, strings.TrimSpace(string(out)))
		}
		if err == nil {
			status := GetStatus(ctx)
			if status.Installed {
				return InstallResult{
					Success: true,
					Status:  status,
					Output:  strings.Join(logs, "\n"),
				}
			}
		} else {
			logs = append(logs, fmt.Sprintf("Homebrew install returned error (%v), falling back to direct Flyway CLI archive download...", err))
		}
	}

	// 2. Fallback: Direct download from Maven Central / Redgate archive into WhoDB managed tools dir
	archiveURL, err := flywayArchiveURL(defaultFlywayVersion)
	if err != nil {
		errMsg := err.Error()
		return InstallResult{
			Success: false,
			Status:  GetStatus(ctx),
			Output:  strings.Join(logs, "\n"),
			Error:   errMsg,
		}
	}

	toolsDir, err := managedToolsDir()
	if err != nil {
		return InstallResult{
			Success: false,
			Status:  GetStatus(ctx),
			Output:  strings.Join(logs, "\n"),
			Error:   err.Error(),
		}
	}

	logs = append(logs, fmt.Sprintf("Downloading Flyway CLI %s from %s ...", defaultFlywayVersion, archiveURL))
	if err := downloadAndExtractTarGz(ctx, archiveURL, toolsDir); err != nil {
		return InstallResult{
			Success: false,
			Status:  GetStatus(ctx),
			Output:  strings.Join(logs, "\n"),
			Error:   fmt.Sprintf("failed to download Flyway CLI: %v", err),
		}
	}

	status := GetStatus(ctx)
	if !status.Installed {
		return InstallResult{
			Success: false,
			Status:  status,
			Output:  strings.Join(logs, "\n"),
			Error:   "Flyway archive was extracted, but the flyway binary could not be verified",
		}
	}
	logs = append(logs, fmt.Sprintf("Successfully installed Flyway CLI (%s) at %s", status.Version, status.BinaryPath))
	return InstallResult{
		Success: true,
		Status:  status,
		Output:  strings.Join(logs, "\n"),
	}
}

func findBrewBinary() string {
	candidates := []string{
		"/opt/homebrew/bin/brew",
		"/usr/local/bin/brew",
	}
	if p, err := exec.LookPath("brew"); err == nil && p != "" {
		candidates = append(candidates, p)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

func flywayArchiveURL(version string) (string, error) {
	var platform string
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			platform = "macosx-arm64"
		} else {
			platform = "macosx-x64"
		}
	case "linux":
		if runtime.GOARCH == "arm64" {
			platform = "linux-alpine-arm64"
		} else {
			platform = "linux-x64"
		}
	default:
		return "", fmt.Errorf("automatic archive download is not supported on %s/%s; please install flyway manually", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf(
		"https://repo1.maven.org/maven2/org/flywaydb/flyway-commandline/%s/flyway-commandline-%s-%s.tar.gz",
		version, version, platform,
	), nil
}

func downloadAndExtractTarGz(ctx context.Context, archiveURL string, destDir string) error {
	dlCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	cleanDest := filepath.Clean(destDir)

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		targetPath := filepath.Join(cleanDest, header.Name)
		if !strings.HasPrefix(filepath.Clean(targetPath), cleanDest+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			mode := os.FileMode(header.Mode)
			if mode == 0 {
				mode = 0644
			}
			if filepath.Base(targetPath) == "flyway" || strings.Contains(targetPath, "/jre/bin/") {
				mode |= 0755
			}
			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				_ = outFile.Close()
				return err
			}
			_ = outFile.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			_ = os.Remove(targetPath)
			_ = os.Symlink(header.Linkname, targetPath)
		}
	}
	return nil
}

func expandDirectoryPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "~/") || trimmed == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			if trimmed == "~" {
				return home
			}
			return filepath.Join(home, trimmed[2:])
		}
	}
	return trimmed
}

// migrationDirectories resolves Maven source migration roots before scanning
// a whole project, where target/classes also contains copies of the same SQL.
func migrationDirectories(root string) []string {
	var directories []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		if path != root && (entry.Name() == "target" || strings.HasPrefix(entry.Name(), ".")) {
			return filepath.SkipDir
		}
		if strings.HasSuffix(filepath.ToSlash(path), "/src/main/resources/db/migration") {
			directories = append(directories, path)
			return filepath.SkipDir
		}
		return nil
	})
	if len(directories) == 0 {
		return []string{root}
	}
	sort.Strings(directories)
	return directories
}

// InspectDirectory scans SQL migrations, preferring Maven source migration roots
// when a project directory includes both source files and build output.
func InspectDirectory(rawDir string) DirectoryPreview {
	resolved := expandDirectoryPath(rawDir)
	if resolved == "" {
		return DirectoryPreview{
			Directory: "",
			Exists:    false,
			Error:     "directory path is empty",
		}
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return DirectoryPreview{
			Directory: resolved,
			Exists:    false,
			Error:     err.Error(),
		}
	}
	if !info.IsDir() {
		return DirectoryPreview{
			Directory: resolved,
			Exists:    false,
			Error:     "path is not a directory",
		}
	}

	var files []MigrationFile
	type fingerprint struct {
		name   string
		sql    [32]byte
		config [32]byte
	}
	seen := map[fingerprint]bool{}
	for _, directory := range migrationDirectories(resolved) {
		err := filepath.WalkDir(directory, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				if path != resolved && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".sql") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			config, err := os.ReadFile(path + ".conf")
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			key := fingerprint{name: d.Name(), sql: sha256.Sum256(content), config: sha256.Sum256(config)}
			if seen[key] {
				return nil
			}
			seen[key] = true
			rel, err := filepath.Rel(resolved, path)
			if err != nil {
				rel = d.Name()
			}
			var size int64
			if fi, statErr := d.Info(); statErr == nil {
				size = fi.Size()
			}
			migType, ver, desc := classifyMigrationFilename(d.Name())
			files = append(files, MigrationFile{
				Filename:     d.Name(),
				RelativePath: rel,
				Version:      ver,
				Description:  desc,
				Type:         migType,
				SizeBytes:    size,
			})
			return nil
		})
		if err != nil {
			return DirectoryPreview{Directory: resolved, Exists: true, Error: err.Error()}
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return compareMigrationFiles(files[i], files[j]) < 0
	})

	return DirectoryPreview{
		Directory: resolved,
		Exists:    true,
		Files:     files,
	}
}

// prepareMigrationDirectory gives Flyway the exact deduplicated scan result.
// Preserve relative paths and per-script configuration; never modify source files.
func prepareMigrationDirectory(preview DirectoryPreview) (directory string, err error) {
	directory, err = os.MkdirTemp("", "whodb-flyway-migrations-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(directory)
		}
	}()
	for _, file := range preview.Files {
		for _, suffix := range []string{"", ".conf"} {
			sourcePath := filepath.Join(preview.Directory, file.RelativePath) + suffix
			content, readErr := os.ReadFile(sourcePath)
			if suffix == ".conf" && errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if readErr != nil {
				return directory, readErr
			}
			target := filepath.Join(directory, file.RelativePath) + suffix
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return directory, err
			}
			if err = os.WriteFile(target, content, 0600); err != nil {
				return directory, err
			}
		}
	}
	return directory, nil
}

func classifyMigrationFilename(filename string) (migType, version, description string) {
	if m := versionedPattern.FindStringSubmatch(filename); len(m) == 3 {
		return "VERSIONED", m[1], strings.ReplaceAll(m[2], "_", " ")
	}
	if m := undoPattern.FindStringSubmatch(filename); len(m) == 3 {
		return "UNDO", m[1], strings.ReplaceAll(m[2], "_", " ")
	}
	if m := repeatablePattern.FindStringSubmatch(filename); len(m) == 2 {
		return "REPEATABLE", "-", strings.ReplaceAll(m[1], "_", " ")
	}
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	return "SQL", "-", base
}

func compareMigrationFiles(a, b MigrationFile) int {
	order := map[string]int{
		"VERSIONED":  1,
		"REPEATABLE": 2,
		"UNDO":       3,
		"SQL":        4,
	}
	if order[a.Type] != order[b.Type] {
		return order[a.Type] - order[b.Type]
	}
	if a.Version != b.Version {
		return compareVersionStrings(a.Version, b.Version)
	}
	return strings.Compare(a.Filename, b.Filename)
}

func compareVersionStrings(a, b string) int {
	partsA := strings.FieldsFunc(a, func(r rune) bool { return r == '.' || r == '_' || r == '-' })
	partsB := strings.FieldsFunc(b, func(r rune) bool { return r == '.' || r == '_' || r == '-' })
	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}
	for i := 0; i < maxLen; i++ {
		if i >= len(partsA) {
			return -1
		}
		if i >= len(partsB) {
			return 1
		}
		numA, errA := strconv.Atoi(partsA[i])
		numB, errB := strconv.Atoi(partsB[i])
		if errA == nil && errB == nil {
			if numA != numB {
				return numA - numB
			}
		} else if partsA[i] != partsB[i] {
			return strings.Compare(partsA[i], partsB[i])
		}
	}
	return 0
}

// Execute runs a Flyway CLI action (migrate, info, validate, baseline, repair) against the selected target database.
func Execute(ctx context.Context, req ExecuteRequest) ExecuteResult {
	start := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "migrate"
	}
	switch action {
	case "migrate", "info", "validate", "baseline", "repair":
	default:
		return ExecuteResult{
			Success: false,
			Action:  action,
			Error:   fmt.Sprintf("unsupported Flyway action: %s", action),
		}
	}

	status := GetStatus(ctx)
	if !status.Installed || status.BinaryPath == "" {
		return ExecuteResult{
			Success: false,
			Action:  action,
			Error:   "Flyway CLI is not installed. Please install Flyway CLI first.",
		}
	}

	dirPreview := InspectDirectory(req.Directory)
	if !dirPreview.Exists || dirPreview.Error != "" {
		errMsg := dirPreview.Error
		if errMsg == "" {
			errMsg = fmt.Sprintf("migration directory does not exist: %s", req.Directory)
		}
		return ExecuteResult{
			Success: false,
			Action:  action,
			Error:   errMsg,
		}
	}

	if req.Credentials == nil {
		return ExecuteResult{
			Success: false,
			Action:  action,
			Error:   "target database credentials are required",
		}
	}

	migrationDir, err := prepareMigrationDirectory(dirPreview)
	if err != nil {
		return ExecuteResult{Success: false, Action: action, Error: err.Error()}
	}
	defer func() { _ = os.RemoveAll(migrationDir) }()

	jdbcURL, user, password, cleanup, err := buildTargetJDBCConnection(ctx, req.Credentials, req.TargetDatabase)
	if err != nil {
		return ExecuteResult{
			Success: false,
			Action:  action,
			Error:   err.Error(),
		}
	}
	defer cleanup()

	args := []string{
		"-url=" + jdbcURL,
		"-locations=filesystem:" + migrationDir,
		"-connectRetries=2",
	}
	if user != "" {
		args = append(args, "-user="+user)
	}
	if password != "" {
		args = append(args, "-password="+password)
	}
	if schema := strings.TrimSpace(req.TargetSchema); schema != "" {
		args = append(args, "-schemas="+schema)
	}
	if req.BaselineOnMigrate {
		args = append(args, "-baselineOnMigrate=true")
	}
	if req.OutOfOrder {
		args = append(args, "-outOfOrder=true")
	}
	if req.ValidateOnMigrate {
		args = append(args, "-validateOnMigrate=true")
	}
	if req.migrationTarget != "" {
		args = append(args, "-target="+req.migrationTarget)
	}
	if req.outputJSON {
		args = append(args, "-outputType=json")
	}
	if req.skipCallbacks {
		recoveryArgs, configErr := recoveryCommandOptions(migrationDir)
		if configErr != nil {
			return ExecuteResult{Success: false, Action: action, Error: configErr.Error()}
		}
		args = append(args, recoveryArgs...)
	}
	args = append(args, action)

	maskedArgs := make([]string, len(args))
	for i, arg := range args {
		if strings.HasPrefix(arg, "-password=") {
			maskedArgs[i] = "-password=******"
		} else {
			maskedArgs[i] = arg
		}
	}
	commandPreview := status.BinaryPath + " " + strings.Join(maskedArgs, " ")

	execCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(execCtx, status.BinaryPath, args...)
	cmd.Dir = dirPreview.Directory
	cmd.Env = buildCommandEnv()
	if req.skipCallbacks {
		cmd.Dir = migrationDir
		cmd.Env = recoveryCommandEnv(cmd.Env)
	}
	out, runErr := cmd.CombinedOutput()
	durationMs := int(time.Since(start).Milliseconds())
	outputStr := strings.TrimSpace(string(out))

	if runErr != nil {
		errMsg := runErr.Error()
		if outputStr != "" {
			errMsg = extractFlywayErrorMessage(outputStr, errMsg)
		}
		return ExecuteResult{
			Success:    false,
			Action:     action,
			Command:    commandPreview,
			Output:     outputStr,
			Error:      errMsg,
			DurationMs: durationMs,
		}
	}

	return ExecuteResult{
		Success:    true,
		Action:     action,
		Command:    commandPreview,
		Output:     outputStr,
		DurationMs: durationMs,
	}
}

func extractFlywayErrorMessage(output string, fallback string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ERROR:") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "ERROR:"))
		}
	}
	return fallback
}

func buildTargetJDBCConnection(ctx context.Context, creds *source.Credentials, overrideDB string) (jdbcURL string, user string, password string, cleanup func(), err error) {
	cleanup = func() {}
	values := creds.Values
	if values == nil {
		values = map[string]string{}
	}

	host := strings.TrimSpace(values["Hostname"])
	if host == "" {
		host = "localhost"
	}
	port := strings.TrimSpace(values["Port"])
	user = strings.TrimSpace(values["Username"])
	password = values["Password"]
	dbName := strings.TrimSpace(overrideDB)
	if dbName == "" {
		dbName = strings.TrimSpace(values["Database"])
	}

	projectID := strings.TrimSpace(values["ProjectID"])
	if projectID == "" {
		projectID = strings.TrimSpace(values["EnvironmentSlug"])
	}
	if projectID != "" && projectID != "__local__" {
		opsplane.RegisterEnvironmentTargetProxy(projectID, host, port)
	}

	sourceType := strings.TrimSpace(creds.SourceType)
	defaultPort := defaultPortForType(sourceType)
	if port == "" {
		port = defaultPort
	}

	// If this target routes through an Ops Control Plane SOCKS5/HTTP proxy, spin up an ephemeral
	// local TCP bridge so external Flyway CLI connects through WhoDB's proxy tunnel seamlessly.
	if port != "" {
		targetAddr := net.JoinHostPort(host, port)
		if _, usesProxy := opsplane.LookupTargetProxy(targetAddr); usesProxy {
			localHost, localPort, closeBridge, bridgeErr := startEphemeralProxyBridge(ctx, targetAddr)
			if bridgeErr == nil {
				host = localHost
				port = localPort
				cleanup = closeBridge
			}
		}
	}

	switch sourceType {
	case "Postgres":
		if dbName == "" {
			dbName = "postgres"
		}
		jdbcURL = fmt.Sprintf("jdbc:postgresql://%s:%s/%s", host, port, url.PathEscape(dbName))
	case "MySQL":
		if dbName == "" {
			return "", "", "", cleanup, errors.New("MySQL target database name is required for Flyway migration")
		}
		jdbcURL = fmt.Sprintf("jdbc:mysql://%s:%s/%s?allowPublicKeyRetrieval=true&useSSL=false", host, port, url.PathEscape(dbName))
	case "MariaDB":
		if dbName == "" {
			return "", "", "", cleanup, errors.New("MariaDB target database name is required for Flyway migration")
		}
		jdbcURL = fmt.Sprintf("jdbc:mariadb://%s:%s/%s", host, port, url.PathEscape(dbName))
	case "ClickHouse":
		if dbName == "" {
			dbName = "default"
		}
		httpPort := strings.TrimSpace(values["HTTPPort"])
		if httpPort == "" {
			httpPort = "8123"
		}
		jdbcURL = fmt.Sprintf("jdbc:clickhouse://%s:%s/%s", host, httpPort, url.PathEscape(dbName))
	case "Sqlite3":
		if dbName == "" {
			return "", "", "", cleanup, errors.New("SQLite database file path is required")
		}
		jdbcURL = "jdbc:sqlite:" + dbName
	case "DuckDB":
		if dbName == "" {
			return "", "", "", cleanup, errors.New("DuckDB database file path is required")
		}
		jdbcURL = "jdbc:duckdb:" + dbName
	default:
		return "", "", "", cleanup, fmt.Errorf("Flyway CLI does not support database type %s", sourceType)
	}

	return jdbcURL, user, password, cleanup, nil
}

func defaultPortForType(sourceType string) string {
	switch sourceType {
	case "Postgres":
		return "5432"
	case "MySQL", "MariaDB":
		return "3306"
	case "ClickHouse":
		return "9000"
	default:
		return ""
	}
}

func startEphemeralProxyBridge(ctx context.Context, targetAddr string) (host string, port string, cleanup func(), err error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", func() {}, err
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return "", "", func() {}, errors.New("unexpected listener address type")
	}

	bridgeCtx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			clientConn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				remoteConn, dialErr := opsplane.DialContext(bridgeCtx, "tcp", targetAddr)
				if dialErr != nil {
					return
				}
				defer remoteConn.Close()
				done := make(chan struct{}, 2)
				go func() {
					_, _ = io.Copy(remoteConn, c)
					done <- struct{}{}
				}()
				go func() {
					_, _ = io.Copy(c, remoteConn)
					done <- struct{}{}
				}()
				<-done
			}(clientConn)
		}
	}()

	cleanup = func() {
		cancel()
		_ = listener.Close()
	}
	return "127.0.0.1", strconv.Itoa(tcpAddr.Port), cleanup, nil
}
