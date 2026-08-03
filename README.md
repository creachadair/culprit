# Culprit Finder

This is a command-line tool for general-purpose bisection search.  Given a pair
of integer values (typically changelist numbers) that bracket a change in
status from working (GOOD) to non-working (BAD) or vice versa, this tool does a
binary search by invoking the specified script for each probe value.

If the script succeeds, the probe is considered GOOD; otherwise BAD.  Search
continues until adjacent values are found that bracket the GOOD/BAD divide.

Here is a trivial example, just to illustrate usage:

```shell
$ culprit -good 1 -bad 50 '(($PROBE < 33))'
```

Here is a more interesting example, automatically bisecting a Git repo:

```shell
$ git checkout -b testing
$ culprit -good 0 -bad 100 '
git reset --hard master~$PROBE
go test -race -cpu=1,2 ./...'
```

Typical output looks like this:

```
Using 0 as GOOD, using 100 as BAD
▷ Verifying that 100 is BAD...
 ✗ 100 is BAD	[1.905077328s elapsed]
Current state: lo=0 [GOOD] hi=100 [BAD]; next=50 Δ=100
 ✗ 50 is BAD	[640.467476ms elapsed]
Current state: lo=0 [GOOD] hi=50 [BAD]; next=25 Δ=50
 ✓ 25 is GOOD	[444.706363ms elapsed]
Current state: lo=25 [GOOD] hi=50 [BAD]; next=37 Δ=25
 ✓ 37 is GOOD	[396.818592ms elapsed]
Current state: lo=37 [GOOD] hi=50 [BAD]; next=43 Δ=13
 ✗ 43 is BAD	[582.092853ms elapsed]
Current state: lo=37 [GOOD] hi=43 [BAD]; next=40 Δ=6
 ✓ 40 is GOOD	[904.856495ms elapsed]
Current state: lo=40 [GOOD] hi=43 [BAD]; next=41 Δ=3
 ✓ 41 is GOOD	[659.378398ms elapsed]
Current state: lo=41 [GOOD] hi=43 [BAD]; next=42 Δ=2
 ✗ 42 is BAD	[535.067134ms elapsed]
▷ Culprit found:
  Before: 41 [GOOD]
  After:  42 [BAD]
7 probes; total time elapsed: 4.163573111s
```

This tells us that `HEAD~42` was problematic. The problem may have been
introduced earlier, which we can find by doing a bracketing search backward
from the bad offset (42):

```shell
$ culprit -bad 42 -bracket 'git checkout master
git branch -D culprit 2>/dev/null
git checkout -b culprit HEAD~$PROBE
env GO111MODULE=off git go test'
```
