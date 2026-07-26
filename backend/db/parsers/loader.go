// Package parsers holds the definitions for the application's built-in
// (non-user-created) log parsers. The canonical copies ship embedded in the
// binary (see defaults/*.json) so the app works out of the box, but at
// startup they are bootstrapped into an operator-editable directory
// (PARSER_DEFS_DIR) so parsers can be tuned or extended without a rebuild -
// see LoadAll.
package parsers

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
)

//go:embed defaults/*.json
var embeddedDefaults embed.FS

const embeddedDefaultsDir = "defaults"

// LoadAll returns the full set of built-in parser seeds. When dir is empty,
// it reads only the factory defaults embedded in the binary. When dir is
// set, it bootstraps that directory from the embedded defaults on first run
// (leaving any existing files alone) and then reads *.json from it instead,
// so an operator can edit, add, or remove parser definitions on disk without
// rebuilding the binary. A malformed file is skipped (not fatal) and
// reported via the returned errors so one bad edit doesn't take down every
// other parser.
func LoadAll(dir string) ([]ParserSeed, []error) {
	if dir == "" {
		return loadFromFS(embeddedDefaults, embeddedDefaultsDir)
	}

	if err := bootstrapDir(dir); err != nil {
		seeds, errs := loadFromFS(embeddedDefaults, embeddedDefaultsDir)
		errs = append(errs, fmt.Errorf("bootstrap parser defs dir %s (falling back to embedded defaults): %w", dir, err))
		return seeds, errs
	}
	return loadFromDir(dir)
}

// bootstrapDir seeds dir with the embedded factory-default JSON files the
// first time it's used (empty or missing directory), and leaves it alone on
// every subsequent start so operator edits are never overwritten.
func bootstrapDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		entries = nil
	}

	hasJSON := false
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			hasJSON = true
			break
		}
	}
	if hasJSON {
		return nil
	}

	defaults, err := embeddedDefaults.ReadDir(embeddedDefaultsDir)
	if err != nil {
		return err
	}
	for _, d := range defaults {
		if d.IsDir() {
			continue
		}
		data, err := embeddedDefaults.ReadFile(path.Join(embeddedDefaultsDir, d.Name()))
		if err != nil {
			return fmt.Errorf("read embedded default %s: %w", d.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, d.Name()), data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", d.Name(), err)
		}
	}
	return nil
}

func loadFromDir(dir string) ([]ParserSeed, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []error{fmt.Errorf("read parser defs dir %s: %w", dir, err)}
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var seeds []ParserSeed
	var errs []error
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", name, err))
			continue
		}
		var fileSeeds []ParserSeed
		if err := json.Unmarshal(data, &fileSeeds); err != nil {
			errs = append(errs, fmt.Errorf("parse %s: %w", name, err))
			continue
		}
		seeds = append(seeds, fileSeeds...)
	}
	return seeds, errs
}

func loadFromFS(fsys embed.FS, dir string) ([]ParserSeed, []error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return nil, []error{fmt.Errorf("read embedded parser defs: %w", err)}
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var seeds []ParserSeed
	var errs []error
	for _, name := range names {
		data, err := fsys.ReadFile(path.Join(dir, name))
		if err != nil {
			errs = append(errs, fmt.Errorf("read embedded %s: %w", name, err))
			continue
		}
		var fileSeeds []ParserSeed
		if err := json.Unmarshal(data, &fileSeeds); err != nil {
			errs = append(errs, fmt.Errorf("parse embedded %s: %w", name, err))
			continue
		}
		seeds = append(seeds, fileSeeds...)
	}
	return seeds, errs
}
