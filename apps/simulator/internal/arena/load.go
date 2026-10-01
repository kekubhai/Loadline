package arena

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// definitionsFS holds the canonical challenge definitions. They are
// version-controlled JSON, compiled into the binary so a running server can
// never disagree with what is committed. The challenges table is a
// projection of these files, refreshed at boot.
//
//go:embed definitions/*.json
var definitionsFS embed.FS

const definitionsDir = "definitions"

// Definitions returns every embedded challenge definition, validated and in
// deterministic order (difficulty, then name, then slug). A malformed file
// is an error, not a skipped entry: a broken challenge must fail loudly at
// boot rather than appear half-configured to a user.
func Definitions() ([]Definition, error) {
	entries, err := fs.ReadDir(definitionsFS, definitionsDir)
	if err != nil {
		return nil, fmt.Errorf("arena: read embedded definitions: %w", err)
	}
	out := make([]Definition, 0, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := definitionsFS.ReadFile(definitionsDir + "/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("arena: read %s: %w", e.Name(), err)
		}
		var def Definition
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&def); err != nil {
			return nil, fmt.Errorf("arena: parse %s: %w", e.Name(), err)
		}
		if err := def.Validate(); err != nil {
			return nil, fmt.Errorf("arena: %s: %w", e.Name(), err)
		}
		if seen[def.Slug] {
			return nil, fmt.Errorf("arena: duplicate challenge slug %q", def.Slug)
		}
		seen[def.Slug] = true
		out = append(out, def)
	}
	sort.Slice(out, func(i, j int) bool {
		if difficultyRank(out[i].Difficulty) != difficultyRank(out[j].Difficulty) {
			return difficultyRank(out[i].Difficulty) < difficultyRank(out[j].Difficulty)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// BySlug returns the embedded definition for a slug.
func BySlug(slug string) (Definition, bool) {
	defs, err := Definitions()
	if err != nil {
		return Definition{}, false
	}
	for _, d := range defs {
		if d.Slug == slug {
			return d, true
		}
	}
	return Definition{}, false
}

func difficultyRank(d Difficulty) int {
	switch d {
	case DifficultyBeginner:
		return 0
	case DifficultyIntermediate:
		return 1
	case DifficultyAdvanced:
		return 2
	case DifficultyExpert:
		return 3
	default:
		return 4
	}
}
