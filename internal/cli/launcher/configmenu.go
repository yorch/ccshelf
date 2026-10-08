package launcher

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
)

// configMenu is "ccshelf config" in a terminal. Each choice gathers its values
// with prompts and ends in the same summary and default-no confirmation as the
// flag form, then prints the equivalent flag command.
func (l *launcher) configMenu(ctx context.Context, cc *clicore.Context) error {
	// Fail before asking anything when there is nothing to change.
	if _, err := openConfigFile(cc); err != nil {
		return err
	}
	opts := []ui.Option{
		{Label: "Add a profile source", Value: "add"},
		{Label: "Change a source's pin", Value: "pin"},
		{Label: "Remove a source", Value: "rm"},
		{Label: "Change a setting", Value: "set"},
		{Label: "Open in editor", Value: "edit"},
		{Label: "Done", Value: "done"},
	}
	for {
		i, err := cc.Prompt.Select(ctx, ui.Question{Title: "What do you want to change?", Options: opts})
		if err != nil {
			return err
		}
		if i < 0 || i >= len(opts) {
			return ui.Failure(fmt.Errorf("invalid selection %d", i))
		}
		switch opts[i].Value {
		case "add":
			err = l.sourceAdd(ctx, cc, nil, &sourceAddFlags{})
		case "pin":
			err = l.sourcePin(ctx, cc, nil, "", false)
		case "rm":
			err = l.sourceRm(ctx, cc, nil, false)
		case "set":
			err = l.setOrUnset(ctx, cc, nil, false, false)
		case "edit":
			err = l.editConfig(ctx, cc, false, false, ui.NewRecorder("config", "edit"))
		default:
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// askSource asks for a new source and records the flags that give the same.
func askSource(ctx context.Context, cc *clicore.Context, cfg *config.Config) (config.SourceConfig, *ui.Recorder, error) {
	types := []ui.Option{
		{Label: "Org data repo (git)", Detail: "a repository of profiles, pinned to a tag or commit", Value: config.SourceGit},
		{Label: "Local directory", Detail: "an absolute folder of profiles", Value: config.SourceDir},
		{Label: "Plugin", Detail: "profiles shipped in an installed plugin", Value: config.SourcePlugin},
	}
	i, err := cc.Prompt.Select(ctx, ui.Question{Title: "Kind of profile source", Options: types})
	if err != nil {
		return config.SourceConfig{}, nil, err
	}
	if i < 0 || i >= len(types) {
		return config.SourceConfig{}, nil, ui.Failure(fmt.Errorf("invalid selection %d", i))
	}
	rec := ui.NewRecorder("config", "source", "add")
	var src config.SourceConfig
	switch types[i].Value {
	case config.SourceGit:
		url, err := cc.Prompt.Input(ctx, "Repository URL (nothing is fetched or trusted here)", "", config.ValidateGitURL)
		if err != nil {
			return src, nil, err
		}
		ref, err := cc.Prompt.Input(ctx, "Tag or full commit id to pin it to", "", refValidator(cfg))
		if err != nil {
			return src, nil, err
		}
		path, err := cc.Prompt.Input(ctx, "Folder inside the repo that holds the profiles", "profiles", folderValidator)
		if err != nil {
			return src, nil, err
		}
		src = config.SourceConfig{Type: config.SourceGit, URL: strings.TrimSpace(url), Ref: ref, Path: path}
		rec.Flag("--git-url", src.URL)
		rec.Flag("--ref", ref)
		if path != "profiles" {
			rec.Flag("--path", path)
		}
	case config.SourceDir:
		dir, err := cc.Prompt.Input(ctx, "Absolute directory of profiles", "", func(s string) error {
			return cfg.ValidateSource(config.SourceConfig{Type: config.SourceDir, Path: strings.TrimSpace(s)})
		})
		if err != nil {
			return src, nil, err
		}
		src = config.SourceConfig{Type: config.SourceDir, Path: strings.TrimSpace(dir)}
		rec.Flag("--dir", src.Path)
	default:
		id, err := cc.Prompt.Input(ctx, "Plugin (name@marketplace)", "", func(s string) error {
			if !validPluginID(s) {
				return errors.New("use name@marketplace")
			}
			return nil
		})
		if err != nil {
			return src, nil, err
		}
		mp, err := cc.Prompt.Input(ctx, "Marketplace it must come from (owner/repo or git URL; Enter to skip)", "", func(s string) error {
			if s == "" {
				return nil
			}
			return config.ValidateMarketplaceSource(s)
		})
		if err != nil {
			return src, nil, err
		}
		path, err := cc.Prompt.Input(ctx, "Folder inside the plugin that holds the profiles (Enter for the default)", "", func(s string) error {
			if s == "" {
				return nil
			}
			return folderValidator(s)
		})
		if err != nil {
			return src, nil, err
		}
		src = config.SourceConfig{Type: config.SourcePlugin, Plugin: id, Marketplace: mp, Path: path}
		rec.Flag("--plugin", id)
		if mp != "" {
			rec.Flag("--marketplace", mp)
		}
		if path != "" {
			rec.Flag("--path", path)
		}
	}
	rec.Bool("--yes")
	return src, rec, nil
}

func folderValidator(s string) error {
	if s == "" {
		return errors.New("a folder is required")
	}
	return config.ValidateSourceFolder(s)
}

// pickSource asks which source; only filter-type sources are offered when
// filter is not empty.
func pickSource(ctx context.Context, cc *clicore.Context, cfg *config.Config, title, filter string) (int, error) {
	var opts []ui.Option
	var idx []int
	for i, s := range cfg.Sources {
		if filter != "" && s.Type != filter {
			continue
		}
		detail := s.Type
		if s.Ref != "" {
			detail += " @ " + s.Ref
		}
		opts = append(opts, ui.Option{Label: fmt.Sprintf("%d. %s", i+1, ui.SanitizeLine(s.SourceLocation())), Detail: ui.SanitizeLine(detail), Value: fmt.Sprint(i + 1)})
		idx = append(idx, i)
	}
	if len(opts) == 0 {
		what := "no profile sources are configured"
		if filter != "" {
			what = "there is no " + filter + " source"
		}
		return -1, ui.Failure(withHint(errors.New(what), "add one with: ccshelf config source add"))
	}
	i, err := cc.Prompt.Select(ctx, ui.Question{Title: title, Options: opts, Filterable: true})
	if err != nil {
		return -1, err
	}
	if i < 0 || i >= len(opts) {
		return -1, ui.Failure(fmt.Errorf("invalid selection %d", i))
	}
	return idx[i], nil
}

// pickSetting asks which allowlisted key to change.
func pickSetting(ctx context.Context, cc *clicore.Context, cfg *config.Config) (string, error) {
	var opts []ui.Option
	for _, st := range config.Settings() {
		v, _ := cfg.GetSetting(st.Key)
		if v == "" {
			v = "(default)"
		}
		opts = append(opts, ui.Option{Label: st.Key, Detail: ui.SanitizeLine(v) + ": " + st.Help, Value: st.Key})
	}
	i, err := cc.Prompt.Select(ctx, ui.Question{Title: "Setting to change", Options: opts, Filterable: true})
	if err != nil {
		return "", err
	}
	if i < 0 || i >= len(opts) {
		return "", ui.Failure(fmt.Errorf("invalid selection %d", i))
	}
	return opts[i].Value, nil
}

// askSettingValue asks the new value of key. For a value that has a default an
// empty answer (or the "default" choice) unsets the key.
func askSettingValue(ctx context.Context, cc *clicore.Context, cfg *config.Config, key string) (value string, unset bool, err error) {
	st, _ := config.LookupSetting(key)
	cur, _ := cfg.GetSetting(key)
	if len(st.Values) > 0 {
		opts := make([]ui.Option, 0, len(st.Values))
		for _, v := range st.Values {
			d := ""
			if v == cur {
				d = "current"
			}
			opts = append(opts, ui.Option{Label: v, Detail: d, Value: v})
		}
		i, err := cc.Prompt.Select(ctx, ui.Question{Title: "New value of " + key, Options: opts})
		if err != nil {
			return "", false, err
		}
		if i < 0 || i >= len(opts) {
			return "", false, ui.Failure(fmt.Errorf("invalid selection %d", i))
		}
		return opts[i].Value, false, nil
	}
	v, err := cc.Prompt.Input(ctx, "New value of "+key+" (empty to go back to the default)", "", func(s string) error {
		if s == "" {
			return nil
		}
		probe := *cfg
		return probe.SetSetting(key, s)
	})
	if err != nil {
		return "", false, err
	}
	if v = strings.TrimSpace(v); v == "" {
		return "", true, nil
	}
	return v, false, nil
}
