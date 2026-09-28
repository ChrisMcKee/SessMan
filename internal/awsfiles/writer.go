package awsfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Credentials holds temporary AWS credentials for a named profile.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Writer manages ~/.aws/credentials and ~/.aws/config merges.
type Writer struct {
	mu              sync.Mutex
	credentialsPath string
	configPath      string
}

// NewWriter uses the default ~/.aws paths.
func NewWriter() (*Writer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".aws")
	return &Writer{
		credentialsPath: filepath.Join(dir, "credentials"),
		configPath:      filepath.Join(dir, "config"),
	}, nil
}

// NewWriterAt is for tests.
func NewWriterAt(credentialsPath, configPath string) *Writer {
	return &Writer{
		credentialsPath: credentialsPath,
		configPath:      configPath,
	}
}

// UpsertProfile writes credentials and optional region for a named profile.
func (w *Writer) UpsertProfile(profile string, creds Credentials, region string, syncRegion bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := ensureParent(w.credentialsPath); err != nil {
		return err
	}
	if err := ensureParent(w.configPath); err != nil {
		return err
	}

	credKeys := map[string]string{
		"aws_access_key_id":     creds.AccessKeyID,
		"aws_secret_access_key": creds.SecretAccessKey,
		"aws_session_token":     creds.SessionToken,
	}
	if err := upsertINI(w.credentialsPath, profileSection(profile, false), credKeys); err != nil {
		return fmt.Errorf("update credentials: %w", err)
	}

	if syncRegion && region != "" {
		cfgKeys := map[string]string{"region": region}
		if err := upsertINI(w.configPath, profileSection(profile, true), cfgKeys); err != nil {
			return fmt.Errorf("update config: %w", err)
		}
	}
	return nil
}

// SetProfileRegion writes region into ~/.aws/config for a profile without
// touching credentials.
func (w *Writer) SetProfileRegion(profile, region string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if region == "" {
		return nil
	}
	if err := ensureParent(w.configPath); err != nil {
		return err
	}
	if err := upsertINI(w.configPath, profileSection(profile, true), map[string]string{"region": region}); err != nil {
		return fmt.Errorf("update config: %w", err)
	}
	return nil
}

// RemoveProfile removes managed credential (and optionally config) entries.
func (w *Writer) RemoveProfile(profile string, removeConfig bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := removeINISection(w.credentialsPath, profileSection(profile, false)); err != nil {
		return fmt.Errorf("remove credentials profile: %w", err)
	}
	if removeConfig {
		if err := removeINISection(w.configPath, profileSection(profile, true)); err != nil {
			return fmt.Errorf("remove config profile: %w", err)
		}
	}
	return nil
}

func profileSection(profile string, isConfig bool) string {
	if !isConfig {
		return profile
	}
	if profile == "default" {
		return "default"
	}
	return "profile " + profile
}

func ensureParent(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

// iniFile is a line-oriented view of an AWS shared config/credentials file.
// Edits touch only the targeted section so comments, blank lines, nested
// settings (e.g. "s3 =" blocks) and unrelated sections survive untouched.
type iniFile struct {
	lines []string
	eol   string
}

func loadINI(path string) (*iniFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &iniFile{eol: "\n"}, nil
		}
		return nil, err
	}
	text := string(data)
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	f := &iniFile{eol: eol}
	if text != "" {
		f.lines = strings.Split(text, "\n")
	}
	return f, nil
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

func isComment(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";")
}

func headerName(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if len(t) >= 2 && t[0] == '[' && t[len(t)-1] == ']' {
		return strings.TrimSpace(t[1 : len(t)-1]), true
	}
	return "", false
}

// topLevelKey returns the key of an unindented "key = value" line.
func topLevelKey(line string) (string, bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' || isComment(line) {
		return "", false
	}
	k, _, ok := strings.Cut(line, "=")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(k), true
}

// find returns the header index and the index of the next header (or EOF).
func (f *iniFile) find(section string) (start, end int, ok bool) {
	start = -1
	for i, l := range f.lines {
		name, isHeader := headerName(l)
		if !isHeader {
			continue
		}
		if start >= 0 {
			return start, i, true
		}
		if name == section {
			start = i
		}
	}
	if start >= 0 {
		return start, len(f.lines), true
	}
	return 0, 0, false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (f *iniFile) upsert(section string, keys map[string]string) {
	start, end, ok := f.find(section)
	if !ok {
		if len(f.lines) > 0 && !isBlank(f.lines[len(f.lines)-1]) {
			f.lines = append(f.lines, "")
		}
		f.lines = append(f.lines, "["+section+"]")
		for _, k := range sortedKeys(keys) {
			f.lines = append(f.lines, k+" = "+keys[k])
		}
		return
	}
	done := map[string]bool{}
	for i := start + 1; i < end; i++ {
		k, ok := topLevelKey(f.lines[i])
		if !ok {
			continue
		}
		if v, want := keys[k]; want {
			f.lines[i] = k + " = " + v
			done[k] = true
		}
	}
	var added []string
	for _, k := range sortedKeys(keys) {
		if !done[k] {
			added = append(added, k+" = "+keys[k])
		}
	}
	if len(added) == 0 {
		return
	}
	at := end
	for at > start+1 && (isBlank(f.lines[at-1]) || isComment(f.lines[at-1])) {
		at--
	}
	f.lines = slices.Insert(f.lines, at, added...)
}

// remove deletes every occurrence of the section; reports whether anything changed.
func (f *iniFile) remove(section string) bool {
	changed := false
	for {
		start, end, ok := f.find(section)
		if !ok {
			break
		}
		changed = true
		// Blank/comment lines trailing the body most likely belong to the next section.
		stop := end
		for stop > start+1 && (isBlank(f.lines[stop-1]) || isComment(f.lines[stop-1])) {
			stop--
		}
		f.lines = slices.Delete(f.lines, start, stop)
	}
	if changed {
		for len(f.lines) > 0 && isBlank(f.lines[0]) {
			f.lines = f.lines[1:]
		}
		for len(f.lines) > 0 && isBlank(f.lines[len(f.lines)-1]) {
			f.lines = f.lines[:len(f.lines)-1]
		}
	}
	return changed
}

// save writes atomically, following symlinks (dotfile managers) and keeping the file mode.
func (f *iniFile) save(path string) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(target); err == nil {
		mode = st.Mode().Perm()
	}
	content := strings.Join(f.lines, f.eol)
	if len(f.lines) > 0 {
		content += f.eol
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// upsertINI merges keys into a named section, leaving everything else as-is.
func upsertINI(path, section string, keys map[string]string) error {
	f, err := loadINI(path)
	if err != nil {
		return err
	}
	f.upsert(section, keys)
	return f.save(path)
}

func removeINISection(path, section string) error {
	f, err := loadINI(path)
	if err != nil {
		return err
	}
	if !f.remove(section) {
		return nil
	}
	return f.save(path)
}
