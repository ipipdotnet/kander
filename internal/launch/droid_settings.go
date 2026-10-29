package launch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dualface/kander/internal/fs"
)

// droidSettingsFile materializes an immutable, content-addressed runtime settings
// file. The cache survives the launcher process: a terminal may consume argv
// later, and deleting a settings file when pane run returns would race startup.
// Only model selection lives here, never credentials or user settings. Empty
// values are omitted so Droid retains its own defaults.
func droidSettingsFile(template []string, model, effort string) (string, error) {
	needed := false
	for _, arg := range template {
		needed = needed || strings.Contains(arg, "{droid_settings}")
	}
	if !needed {
		return "", nil
	}
	settings := map[string]string{}
	if model != "" {
		settings["model"] = model
	}
	if effort != "" {
		settings["reasoningEffort"] = effort
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(cache, "kander", "droid-settings")
	if err := fs.EnsureDirectoryPath(cache); err != nil {
		return "", err
	}
	if err := fs.EnsurePrivateDirectory(cache, root, true); err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	path := filepath.Join(root, hex.EncodeToString(digest[:])+".json")
	if err := fs.WriteTextAtomic(root, path, string(data), false); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	// Never reuse a modified cache object, even if its name still matches.
	file, err := fs.OpenRegularFileIfExists(root, path)
	if err != nil {
		return "", err
	}
	if file == nil {
		return "", launchError("launch.droid_settings_modified", path)
	}
	defer file.Close()
	stored, err := io.ReadAll(io.LimitReader(file, int64(len(data)+1)))
	if err != nil {
		return "", err
	}
	if string(stored) != string(data) {
		return "", launchError("launch.droid_settings_modified", path)
	}
	return path, nil
}
