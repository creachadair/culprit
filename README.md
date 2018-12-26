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

