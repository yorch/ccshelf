package claude

import (
	"bytes"
	"context"
	"fmt"
	"sort"
)

// maxListOutput bounds how much plugin-list output is kept. It is a variable
// only so that tests can lower it; production code never changes it.
var maxListOutput = 32 << 20

// limitedBuffer collects output up to maxListOutput bytes and sets over when
// more arrives. It deliberately does not embed bytes.Buffer: that would
// promote ReadFrom, which io.Copy prefers over Write and which would bypass
// the limit.
type limitedBuffer struct {
	buf  bytes.Buffer
	over bool
}

// Write implements io.Writer, dropping data past the limit.
func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > maxListOutput {
		b.over = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

// Len returns the number of bytes kept.
func (b *limitedBuffer) Len() int { return b.buf.Len() }

// Bytes returns the bytes kept.
func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

// String returns the bytes kept as a string.
func (b *limitedBuffer) String() string { return b.buf.String() }

func sortPlugins(p []Plugin) {
	sort.SliceStable(p, func(i, j int) bool {
		if p[i].ID != p[j].ID {
			return p[i].ID < p[j].ID
		}
		return p[i].Scope < p[j].Scope
	})
}

// runList runs `claude plugin list --json [--available]` in dir (the result
// depends on the working directory) and returns stdout.
func runList(ctx context.Context, bin, dir string, env []string, available bool) ([]byte, error) {
	ctx, cancel := withDefaultTimeout(ctx, DefaultTimeout)
	defer cancel()
	args := []string{"plugin", "list", "--json"}
	if available {
		args = append(args, "--available")
	}
	var stdout, stderr limitedBuffer
	code, err := spawnDir(ctx, dir, bin, args, env, nil, &stdout, &stderr)
	if err != nil {
		return nil, fmt.Errorf("claude plugin list: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("claude plugin list exited %d: %s", code, excerpt(stderr.String(), 300))
	}
	if stdout.over {
		return nil, fmt.Errorf("claude plugin list produced more than %d bytes", maxListOutput)
	}
	return stdout.Bytes(), nil
}

// ListInstalled returns the installed plugins as seen from dir, sorted by ID
// (then scope). env is the child environment (nil inherits); when ctx has no
// deadline a 30 second timeout applies. It takes about a second, so see
// [InstalledCache].
func ListInstalled(ctx context.Context, bin, dir string, env []string) ([]Plugin, error) {
	out, err := runList(ctx, bin, dir, env, false)
	if err != nil {
		return nil, err
	}
	list, err := parseInstalled(out)
	if err != nil {
		return nil, err
	}
	sortPlugins(list)
	return list, nil
}

// ListAvailable returns the installed and the available plugins from
// `claude plugin list --json --available`, both sorted deterministically.
func ListAvailable(ctx context.Context, bin, dir string, env []string) ([]Plugin, []AvailablePlugin, error) {
	out, err := runList(ctx, bin, dir, env, true)
	if err != nil {
		return nil, nil, err
	}
	inst, avail, err := parseAvailable(out)
	if err != nil {
		return nil, nil, err
	}
	sortPlugins(inst)
	sort.SliceStable(avail, func(i, j int) bool { return avail[i].PluginID < avail[j].PluginID })
	return inst, avail, nil
}
