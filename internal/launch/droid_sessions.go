package launch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dualface/kander/internal/fs"
	"github.com/dualface/kander/internal/probe"
)

const droidSessionEntryLimit = 10000

func droidSessionsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".factory", "sessions"), nil
}

// Droid stores sessions one directory below sessions/. Directory names are not
// identities: only a session_start header's cwd and id can bind a new file.
// Snapshot paths, including unfinished files, so completing an old header later
// cannot make an existing session look newly created.
func droidSessionPaths(ctx context.Context) (map[string]struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := droidSessionsRoot()
	if err != nil {
		return nil, err
	}
	dirs, err := fs.ListDirectory(root, root)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]struct{}{}, nil
	}
	if err != nil {
		return nil, err
	}
	paths := map[string]struct{}{}
	count := len(dirs)
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if count > droidSessionEntryLimit {
			return nil, launchError("launch.session_list_output_limit")
		}
		if dir.Kind != fs.KindDirectory {
			continue
		}
		folder := filepath.Join(root, dir.Name)
		files, err := fs.ListDirectory(root, folder)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		count += len(files)
		if count > droidSessionEntryLimit {
			return nil, launchError("launch.session_list_output_limit")
		}
		for _, file := range files {
			if strings.HasSuffix(file.Name, ".jsonl") {
				paths[filepath.Join(folder, file.Name)] = struct{}{}
			}
		}
	}
	return paths, nil
}

func snapshotDroidSessions(timeout time.Duration) (map[string]struct{}, error) {
	ctx, cancel := probe.TimeoutContext(timeout)
	defer cancel()
	return droidSessionPaths(ctx)
}

func droidSessionHeader(path, cwd string) (string, error) {
	root, err := droidSessionsRoot()
	if err != nil {
		return "", err
	}
	file, err := fs.OpenRegularFileIfExists(root, path)
	if err != nil || file == nil {
		return "", err
	}
	defer file.Close()
	line, err := bufio.NewReader(io.LimitReader(file, 16384)).ReadString('\n')
	if err != nil {
		// A new header may still be in flight. Never bind a partial header.
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		CWD  string `json:"cwd"`
	}
	if json.Unmarshal([]byte(line), &header) != nil || header.Type != "session_start" {
		return "", nil
	}
	if !filepath.IsAbs(header.CWD) || canonicalSessionDir(header.CWD) != canonicalSessionDir(cwd) {
		return "", nil
	}
	if !sessionReferenceRe.MatchString(header.ID) || filepath.Base(path) != header.ID+".jsonl" {
		return "", launchError("launch.session_list_invalid")
	}
	return header.ID, nil
}

// A new file in the same project is not enough: another Droid may start
// concurrently. Require the task's launch instruction in a user message too.
func droidSessionForTask(path, cwd string, prefixes []string) (string, error) {
	id, err := droidSessionHeader(path, cwd)
	if err != nil || id == "" {
		return "", err
	}
	root, err := droidSessionsRoot()
	if err != nil {
		return "", err
	}
	file, err := fs.OpenRegularFileIfExists(root, path)
	if err != nil || file == nil {
		return "", err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1024*1024))
	for i := 0; i < 32; i++ {
		var row struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if decoder.Decode(&row) != nil {
			return "", nil
		}
		if row.Type == "message" && row.Message.Role == "user" {
			for _, part := range row.Message.Content {
				if part.Type == "text" && startsWithAny(part.Text, prefixes) {
					return id, nil
				}
			}
			// Only the initial user message identifies this launch.
			return "", nil
		}
	}
	return "", nil
}

func discoverDroidSession(previous map[string]struct{}, taskID, cwd string, timeout time.Duration) (string, error) {
	prefixes := promptPrefixes(taskID, "start", "resume", "takeover")
	ctx, cancel := probe.TimeoutContext(timeout)
	defer cancel()
	deadline := nowFn().Add(timeout)
	for {
		paths, err := droidSessionPaths(ctx)
		if err != nil {
			return "", err
		}
		ids := map[string]struct{}{}
		for path := range paths {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if _, old := previous[path]; old {
				continue
			}
			id, err := droidSessionForTask(path, cwd, prefixes)
			if err != nil {
				return "", err
			}
			if id != "" {
				ids[id] = struct{}{}
			}
		}
		if len(ids) > 1 {
			return "", launchError("launch.multiple_new_sessions_appeared_during_launch", itoa(len(ids)))
		}
		for id := range ids {
			return id, nil
		}
		remaining := deadline.Sub(nowFn())
		if remaining <= 0 {
			return "", launchError("launch.the_newly_started_session_has_not_appeared_yet")
		}
		sleepFn(min(remaining, 100*time.Millisecond))
	}
}
