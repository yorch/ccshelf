package launcher

import (
	"context"
	"path/filepath"

	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/profile/gitsource"
)

type gitOpts = gitsource.Options

// preparedDir adapts a directory source to PreparedSource.
type preparedDir struct{ profile.Source }

func (preparedDir) Prepare(context.Context) error { return nil }

func dirSourceFor(org string) profile.Source {
	return profile.DirSource(profile.KindOrg, filepath.Join(org, "profiles"))
}
