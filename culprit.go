// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

// Binary culprit performs binary search to find a culprit for a change to the
// state of a linear history.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/creachadair/command"
	"github.com/creachadair/flax"
	"github.com/creachadair/mds/mstr"
)

var flags struct {
	Good       int    `flag:"good,Value known to be good (0 to bracket)"`
	Bad        int    `flag:"bad,Value known to be bad (0 to bracket)"`
	Bracket    bool   `flag:"bracket,Enable bracketing"`
	Echo       bool   `flag:"echo,Echo probe command output to stderr"`
	Log        bool   `flag:"log,Log probe commands as executed to stderr"`
	Verify     bool   `flag:"verify,default=true,Verify the starting points as assigned"`
	Chdir      string `flag:"cd,Change to this directory before each probe (with $PROBE)"`
	Marker     string `flag:"env,default=PROBE,Variable with probe value in script environment"`
	UseShell   string `flag:"shell,default=/bin/sh,Shell to use for running scripts"`
	MaxBracket int    `flag:"bmax,Maximum bracketing value"`
	ProbeList  string `flag:"probelist,File containing probe values, one per line"`
}

func main() {
	root := &command.C{
		Name:  command.ProgramName(),
		Usage: "[flags] <script>...",
		Help: `Perform bisection search for a cause of error.

Given a pair of positive integer values representing points in a sequence of
states between which a change in status occurs from working (GOOD) to
non-working (BAD) or vice versa, this tool performs a binary search by invoking
the specified script for each probe value.  If the script succeeds, the probe is
considered GOOD; otherwise BAD.  Search continues until adjacent values are
found that bracket the GOOD/BAD divide.

At least one of --good and --bad must be positive. By default, it probes
between the two values.

However, if --bracket is true and one of the values is 0, the tool will probe
for a bracketing value above the other (positive) value.

If --env is set, an environment variable with that name is populated with the
current probe value when executing the probe script.

If --probelist is set, its contents are used as the probe values rather than
the current index. Each line is one probe value. If only one endpoint is set,
this implicitly sets --bracket also. Lines are addressed from 1.

If --cd is set, the probe script is run with its current working directory set
to the specified value. The variable $PROBE is replaced with the current probe
value in the directory path.`,
		SetFlags: command.Flags(flax.MustBind, &flags),
		Run:      command.Adapt(runMain),
		Commands: []*command.C{
			command.HelpCommand(nil),
			command.VersionCommand(),
		},
	}
	command.RunOrFail(root.NewEnv(nil), os.Args[1:])
}

func runMain(env *command.Env, script []string) error {
	if len(script) == 0 {
		return env.Usagef("you must provide a script to execute")
	}
	if flags.ProbeList != "" {
		data, err := os.ReadFile(flags.ProbeList)
		if err != nil {
			return fmt.Errorf("reading probe list: %w", err)
		}
		lines := mstr.Lines(string(data))
		env.Config = lines
		flags.MaxBracket = len(lines)
		diag(env, "Loaded %d probe strings from %q", len(lines), flags.ProbeList)
	}

	// Establish the endpoints of the search. These may be modified by
	// bracketing (see below).
	if flags.Good < 0 || flags.Bad < 0 {
		return fmt.Errorf("the values of GOOD (%d) and BAD (%d) must be non-negative", flags.Good, flags.Bad)
	} else if flags.Good == flags.Bad {
		return fmt.Errorf("the values of GOOD and BAD must be distinct (got %d)", flags.Good)
	}
	diag(env, "Using %d as GOOD, using %d as BAD", flags.Good, flags.Bad)

	// Order the endpoints so that lo ≤ hi.  If requested, verify that the
	// starting endpoints have the expected status.
	lo, hi, loOK, hiOK := minmax(flags.Good, flags.Bad)

	// If there is a probe list file, and the caller only specified one
	// endpoint, implicitly enable bracketing.
	if env.Config != nil && lo == 0 {
		flags.Bracket = true
	}
	if flags.Verify {
		if lo > 0 {
			diag(env, "▷ Verifying that %d is %v...", lo, loOK)
			if ok, err := runTrial(env, lo, script); err != nil {
				return fmt.Errorf("probe value %d failed: %w", lo, err)
			} else if ok != loOK {
				return fmt.Errorf("value %d reports as %v, but is expected to be %v", lo, ok, loOK)
			}
		}
		diag(env, "▷ Verifying that %d is %v...", hi, hiOK)
		if ok, err := runTrial(env, hi, script); err != nil {
			return fmt.Errorf("probe value %d failed: %w", hi, err)
		} else if ok != hiOK {
			return fmt.Errorf("value %d reports as %v, but is expected to be %v", hi, ok, hiOK)
		}
	}

	// Search for a culprit...
	np := 0 // probe counter
	start := time.Now()

	// Bracketing: If lo == 0, search for a bracketing value above hi.
	if flags.Bracket && lo == 0 {
		// Use hi as the baseline.
		lo, loOK = hi, hiOK

		diag(env, "Searching for a bracketing value above %d [%v]...", lo, loOK)
		delta := clog2(lo)
		base := lo
		for {
			next := lo + delta
			if next <= 0 { // overflow
				return fmt.Errorf("no bracketing value found above lo=%d [%s]", lo, loOK)
			} else if flags.MaxBracket > 0 && next > flags.MaxBracket {
				return fmt.Errorf("no bracketing value found between lo=%d [%s] and %d", lo, loOK, flags.MaxBracket)
			}
			np++

			// If the search brackets a change, we're done.
			diag(env, "Bracketing search: base=%d [%s]; next=%d Δ=%d", base, loOK, next, delta)
			if ok, err := runTrial(env, next, script); err != nil {
				return fmt.Errorf("probe %d failed: %w", next, err)
			} else if ok != loOK {
				hi = next
				hiOK = !loOK
				lo = base
				break
			}
			delta *= 2
			base = next
		}
		diag(env, "Found bracketing value: hi=%d [%s], adjusted lo to %d", hi, hiOK, lo)
	}

	// Binary search in the remaining delta.
	for lo+1 < hi {
		next := (lo + hi) / 2
		np++
		diag(env, "Current state: lo=%d [%s] hi=%d [%s]; next=%d Δ=%d", lo, loOK, hi, hiOK, next, hi-lo)
		ok, err := runTrial(env, next, script)
		if err != nil {
			return fmt.Errorf("probe %d failed: %w", next, err)
		}
		if ok == loOK {
			lo = next
			loOK = ok
		} else {
			hi = next
			hiOK = ok
		}
	}

	// Report on the outcome.
	if lo < hi {
		printCulpritInfo(lo, loOK, hi, hiOK)
	} else {
		fmt.Println("No culprit found")
	}
	diag(env, "%d probes; total time elapsed: %v", np, time.Since(start).Round(time.Millisecond))
	return nil
}

func diag(w io.Writer, msg string, args ...any) { fmt.Fprintf(w, msg+"\n", args...) }

type status bool

// Status markers.
const (
	GOOD status = true
	BAD  status = false
)

func (s status) String() string {
	if s == GOOD {
		return "GOOD"
	}
	return "BAD"
}

func (s status) Mark() rune {
	if s == GOOD {
		return '✓'
	}
	return '✗'
}

func prepCommand(env *command.Env, args []string, probe string) *exec.Cmd {
	script := strings.Join(args, " ")
	logCommand(env, "SCRIPT", script, nil)
	cmd := exec.Command(flags.UseShell)
	cmd.Stdin = strings.NewReader(script)
	if flags.Echo {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	}
	if flags.Marker != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", flags.Marker, probe))
	}
	if flags.Chdir != "" {
		cmd.Dir = os.Expand(flags.Chdir, func(key string) string {
			if key == "PROBE" {
				return probe
			}
			return ""
		})
		logCommand(env, "CHDIR", cmd.Dir, nil)
	}
	return cmd
}

func logCommand(w io.Writer, tag, cmd string, args []string) {
	if flags.Log {
		fmt.Fprintln(w, tag, "::", cmd, strings.Join(args, " "))
	}
}

func runTrial(env *command.Env, cl int, args []string) (out status, err error) {
	start := time.Now()
	defer func() {
		if err == nil {
			diag(env, " %c %d is %v\t[%v elapsed]", out.Mark(), cl, out, time.Since(start).Round(time.Millisecond))
		}
	}()
	probe := strconv.Itoa(cl)
	if probeText, ok := env.Config.([]string); ok {
		if cl <= 0 || cl > len(probeText) {
			return out, fmt.Errorf("invalid probe index %d: no corresponding value", cl)
		}
		probe = probeText[cl-1]
	}
	if err := prepCommand(env, args, probe).Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			return status(e.Success()), nil
		}
		return out, fmt.Errorf("subprocess failed: %w", err)
	}
	return GOOD, nil
}

func minmax(good, bad int) (lo, hi int, loOK, hiOK status) {
	if good > bad {
		return bad, good, BAD, GOOD
	}
	return good, bad, GOOD, BAD
}

// clog2 returns the least k > 0 such that 2^k ≥ z.
func clog2(z int) int {
	k, n := 1, 2
	for n < z {
		k++
		n *= 2
	}
	return k
}

func printCulpritInfo(lo int, loOK status, hi int, hiOK status) {
	fmt.Printf(`▷ Culprit found:
  Before: %d [%s]
  After:  %d [%s]
`, lo, loOK, hi, hiOK)

	// TODO: Add support for printing git logs, since that is a common use case.
}
