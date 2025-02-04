// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

// Binary culprit performs binary search to find a culprit for a change to the
// state of a linear history.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	goodVal  = flag.Int("good", 0, "Value known to be good (0 to bracket)")
	badVal   = flag.Int("bad", 0, "Value known to be bad (0 to bracket)")
	doBrack  = flag.Bool("bracket", false, "Enable bracketing")
	doEcho   = flag.Bool("echo", false, "Echo probe command output to stderr")
	doLog    = flag.Bool("log", false, "Log probe commands as executed to stderr")
	doVerify = flag.Bool("verify", true, "Verify the starting points as assigned")
	doChdir  = flag.String("cd", "", `Change to this directory before each probe (with $PROBE)`)
	clMarker = flag.String("env", "PROBE", "Variable with probe value in script environment")
	inShell  = flag.String("shell", "/bin/sh", "Shell to use for running scripts")
	maxBrack = flag.Int("bmax", 0, "Maximum bracketing value")

	cmdOutput = io.Discard
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: %[1]s [options] <script>...

Given a pair of integer values representing points in a sequence of states
between which a change in status occurs from working (GOOD) to non-working
(BAD) or vice versa, this tool performs a binary search by invoking the
specified script for each probe value.  If the script succeeds, the probe is
considered GOOD; otherwise BAD.  Search continues until adjacent values are
found that bracket the GOOD/BAD divide.

At least one of -good and -bad must be positive. By default, %[1]s probes
between the two values.

However, if -bracket is true and one of the values is 0, the tool will probe
for a bracketing value above the other (positive) value.

If -env is set, an environment variable with that name is populated with the
current probe value when executing the probe script.

If -cd is set, the probe script is run with its current working directory set
to the specified value. The variable $PROBE is replaced with the current probe
value in the directory path.

Options:
`, filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
}

func main() {
	flag.Parse()
	if flag.NArg() == 0 {
		log.Fatal("You must provide a script to execute")
	}
	if *doEcho {
		cmdOutput = os.Stderr
	}

	// Establish the endpoints of the search. These may be modified by
	// bracketing (see below).
	if *goodVal < 0 || *badVal < 0 {
		log.Fatalf("The values of GOOD (%d) and BAD (%d) must be non-negative", *goodVal, *badVal)
	} else if *goodVal == *badVal {
		log.Fatalf("The values of GOOD and BAD must be distinct (got %d)", *goodVal)
	}
	diag("Using %d as GOOD, using %d as BAD", *goodVal, *badVal)

	// Order the endpoints so that lo ≤ hi.  If requested, verify that the
	// starting endpoints have the expected status.
	lo, hi, loOK, hiOK := minmax(*goodVal, *badVal)
	if *doVerify {
		if lo > 0 {
			diag("▷ Verifying that %d is %v...", lo, loOK)
			if ok := runTrial(lo, flag.Args()); ok != loOK {
				log.Fatalf("Value %d reports as %v, but is expected to be %v", lo, ok, loOK)
			}
		}
		diag("▷ Verifying that %d is %v...", hi, hiOK)
		if ok := runTrial(hi, flag.Args()); ok != hiOK {
			log.Fatalf("Value %d reports as %v, but is expected to be %v", hi, ok, hiOK)
		}
	}

	// Search for a culprit...
	np := 0 // probe counter
	start := time.Now()

	// Bracketing: If lo == 0, search for a bracketing value above hi.
	if *doBrack && lo == 0 {
		// Use hi as the baseline.
		lo, loOK = hi, hiOK

		diag("Searching for a bracketing value above %d [%v]...", lo, loOK)
		delta := clog2(lo)
		base := lo
		for {
			next := lo + delta
			if next <= 0 { // overflow
				log.Fatalf("No bracketing value found above lo=%d [%s]", lo, loOK)
			} else if *maxBrack > 0 && next > *maxBrack {
				log.Fatalf("No bracketing value found between lo=%d [%s] and %d", lo, loOK, *maxBrack)
			}
			np++

			// If the search brackets a change, we're done.
			diag("Bracketing search: base=%d [%s]; next=%d Δ=%d", base, loOK, next, delta)
			if runTrial(next, flag.Args()) != loOK {
				hi = next
				hiOK = !loOK
				lo = base
				break
			}
			delta *= 2
			base = next
		}
		diag("Found bracketing value: hi=%d [%s], adjusted lo to %d", hi, hiOK, lo)
	}

	// Binary search in the remaining delta.
	for lo+1 < hi {
		next := (lo + hi) / 2
		np++
		diag("Current state: lo=%d [%s] hi=%d [%s]; next=%d Δ=%d", lo, loOK, hi, hiOK, next, hi-lo)
		ok := runTrial(next, flag.Args())
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
	diag("%d probes; total time elapsed: %v", np, time.Since(start))
}

func diag(msg string, args ...interface{}) { fmt.Fprintf(os.Stderr, msg+"\n", args...) }

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

func prepCommand(args []string, cl int) *exec.Cmd {
	script := strings.Join(args, " ")
	logCommand("SCRIPT", script, nil)
	cmd := exec.Command(*inShell)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = cmdOutput
	cmd.Stderr = cmdOutput
	if *clMarker != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d", *clMarker, cl))
	}
	if *doChdir != "" {
		cmd.Dir = os.Expand(*doChdir, func(key string) string {
			if key == "PROBE" {
				return strconv.Itoa(cl)
			}
			return ""
		})
		logCommand("CHDIR", cmd.Dir, nil)
	}
	return cmd
}

func logCommand(tag, cmd string, args []string) {
	if *doLog {
		fmt.Fprintln(os.Stderr, tag, "::", cmd, strings.Join(args, " "))
	}
}

func runTrial(cl int, args []string) (out status) {
	start := time.Now()
	defer func() {
		diag(" %c %d is %v\t[%v elapsed]", out.Mark(), cl, out, time.Since(start))
	}()

	if err := prepCommand(args, cl).Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			return status(e.Success())
		}
		log.Fatalf("Subprocess failed: %v", err)
	}
	return GOOD
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
