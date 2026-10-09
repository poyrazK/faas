package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zalando/go-keyring"
)

type connectionProfile struct {
	APIBase string `json:"api_base"`
}

var profileOverride string

func selectedProfile(cfg cliConfig) string {
	if profileOverride != "" {
		return profileOverride
	}
	if cfg.ActiveProfile != "" {
		return cfg.ActiveProfile
	}
	return "default"
}
func currentProfile() string { cfg, _ := loadCLIConfig(); return selectedProfile(cfg) }
func profileKeyringAccount() string {
	if name := currentProfile(); name != "default" {
		return "profile:" + name
	}
	return keyringAccount
}
func deleteLegacyKeyring(kr keyringStub) error {
	if currentProfile() != "default" {
		return keyring.ErrNotFound
	}
	return kr.Delete(legacyKeyringService, keyringAccount)
}
func validateProfileName(name string) error {
	if len(name) == 0 || len(name) > 64 {
		return errors.New("profile name must contain 1–64 lowercase letters, digits, underscores or hyphens")
	}
	for _, c := range name {
		valid := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_'
		if !valid {
			return fmt.Errorf("invalid profile name %q", name)
		}
	}
	return nil
}

// Only prefix flags select connections; command-local --profile retains its meaning.
func extractConnectionProfile(args []string) ([]string, error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--json" || arg == "-j" || strings.HasPrefix(arg, "--json=") {
			continue
		}
		if arg == "--profile" || strings.HasPrefix(arg, "--profile=") {
			value := strings.TrimPrefix(arg, "--profile=")
			end := i + 1
			if arg == "--profile" {
				if end == len(args) {
					return args, errors.New("--profile requires a name")
				}
				value = args[end]
				end++
			}
			if profileOverride != "" {
				return args, errors.New("--profile may be specified only once")
			}
			if err := validateProfileName(value); err != nil {
				return args, err
			}
			profileOverride = value
			args = append(append([]string{}, args[:i]...), args[end:]...)
			i--
			continue
		}
		break
	}
	return args, nil
}
func validateSelectedProfile() error {
	cfg, err := loadCLIConfig()
	if err != nil {
		return err
	}
	name := selectedProfile(cfg)
	if err := validateProfileName(name); err != nil {
		return err
	}
	if name != "default" {
		if _, ok := cfg.Profiles[name]; !ok {
			return fmt.Errorf("unknown profile %q; run gregale profile list", name)
		}
	}
	return nil
}
func cmdProfile(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, "usage: gregale profile <add|list|use|remove|check> ...", "config")
		return 1
	}
	cfg, err := loadCLIConfig()
	if err != nil {
		return printErr("Could not read config", err)
	}
	switch args[0] {
	case "check":
		return cmdProfileCheck(args[1:])
	case "list":
		if len(args) != 1 {
			return printErr("Invalid profile usage", errors.New("usage: gregale profile list"))
		}
		names := []string{"default"}
		for name := range cfg.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		rows := []map[string]any{}
		for _, name := range names {
			base := cfg.APIBase
			if base == "" {
				base = defaultAPIBase
			}
			if name != "default" {
				base = cfg.Profiles[name].APIBase
			}
			rows = append(rows, map[string]any{"name": name, "api_base": base, "active": selectedProfile(cfg) == name})
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStdout, "%s  %s  active=%t\n", name, base, selectedProfile(cfg) == name)
			}
		}
		if jsonOutput {
			return jsonOut(writeNDJSON(rows))
		}
		return 0
	case "add", "use", "remove":
		want := 2
		if args[0] == "add" {
			want = 3
		}
		if len(args) < 2 || len(args) != want {
			return printErr("Invalid profile usage", errors.New("use: profile add <name> <api-url>, profile use <name>, or profile remove <name>"))
		}
		name := args[1]
		if err := validateProfileName(name); err != nil {
			return printErr("Invalid profile name", err)
		}
		_, exists := cfg.Profiles[name]
		exists = exists || name == "default"
		switch args[0] {
		case "add":
			if len(args) != 3 {
				return printErr("Invalid profile usage", errors.New("use: profile add <name> <api-url>"))
			}
			if exists {
				return printErr("Profile already exists", errors.New(name))
			}
			base, err := validateConfigAPIBase(args[2])
			if err != nil {
				return printErr("Invalid API URL", err)
			}
			if cfg.Profiles == nil {
				cfg.Profiles = map[string]connectionProfile{}
			}
			cfg.Profiles[name] = connectionProfile{APIBase: base}
		case "use":
			if !exists {
				return printErr("Unknown profile", errors.New(name))
			}
			cfg.ActiveProfile = name
		case "remove":
			if name == "default" {
				return printErr("Cannot remove default profile", errors.New("the default profile preserves existing configuration"))
			}
			if !exists {
				return printErr("Unknown profile", errors.New(name))
			}
			if selectedProfile(cfg) == name || cfg.ActiveProfile == name {
				return printErr("Cannot remove active profile", errors.New("switch to another profile first"))
			}
			old := profileOverride
			profileOverride = name
			deleteToken()
			clearManagedSession()
			profileOverride = old
			delete(cfg.Profiles, name)
		}
		if err := saveCLIConfig(cfg); err != nil {
			return printErr("Could not save profiles", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]string{"profile": name, "action": args[0]}))
		}
		PrintOK(osStdout, "Profile %s: %s.", name, args[0])
		return 0
	default:
		parent, _ := lookupCliCommand("profile")
		return printUnknownSubcommand(osStderr, "profile", parent, args[0])
	}
}

type connectionContext struct {
	Profile       string `json:"profile"`
	ProfileSource string `json:"profile_source"`
	APIBase       string `json:"api_base"`
	APISource     string `json:"api_source"`
}

func effectiveConnectionContext() *connectionContext {
	cfg, _ := loadCLIConfig()
	source := "default"
	if cfg.ActiveProfile != "" {
		source = "config"
	}
	if profileOverride != "" {
		source = "flag:--profile"
	}
	entry, _ := configEntry(effectiveConfigEntries(cfg), configKeyAPIBase)
	return &connectionContext{selectedProfile(cfg), source, entry.Value, entry.Source}
}
func renderConnectionContext() {
	c := effectiveConnectionContext()
	_, _ = fmt.Fprintf(osStdout, "Profile: %s (source=%s)\nAPI: %s (source=%s)\n", c.Profile, c.ProfileSource, c.APIBase, c.APISource)
}

func validateConnectionProfiles(cfg cliConfig) error {
	for name, profile := range cfg.Profiles {
		if err := validateProfileName(name); err != nil {
			return err
		}
		if name == "default" {
			return errors.New("default profile settings belong in api_base")
		}
		normalized, err := validateConfigAPIBase(profile.APIBase)
		if err != nil {
			return fmt.Errorf("profile %s: %w", name, err)
		}
		if normalized != profile.APIBase {
			return fmt.Errorf("profile %s API URL must be normalized", name)
		}
	}
	if cfg.ActiveProfile != "" && cfg.ActiveProfile != "default" {
		if _, ok := cfg.Profiles[cfg.ActiveProfile]; !ok {
			return fmt.Errorf("unknown active profile %q", cfg.ActiveProfile)
		}
	}
	return nil
}
func bindProfileCompletionCache(c *Client) *Client {
	if name := currentProfile(); name != "default" && os.Getenv("FAAS_COMPLETION_CACHE_PATH") == "" {
		cache := c.CompletionCache()
		path := cache.Path()
		cache.SetPath(filepath.Join(filepath.Dir(path), "profiles", name, filepath.Base(path)))
	}
	return c
}

// Completion reads configured names locally and never resolves credentials.
func cmdCompletionProfileNames() int {
	cfg, err := loadCLIConfig()
	if err != nil {
		return 0
	}
	names := []string{"default"}
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = fmt.Fprintln(osStdout, name)
	}
	return 0
}
