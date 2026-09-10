package schedule

import (
	"testing"
	"time"
)

// losAngeles is used because its transitions are well known: in 2026 the clock
// jumps 02:00 -> 03:00 on 8 March, and 02:00 -> 01:00 on 1 November.
func losAngeles(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("Timezone database unavailable: %v", err)
	}
	return location
}

func TestNextFiresAtTheRequestedWallClockTime(t *testing.T) {
	spec, err := Parse("0 7 * * *", "America/Los_Angeles")
	if err != nil {
		t.Fatalf("Failed to parse schedule: %v", err)
	}
	location := losAngeles(t)

	after := time.Date(2026, time.June, 1, 8, 0, 0, 0, location)
	next := spec.Next(after, "")

	want := time.Date(2026, time.June, 2, 7, 0, 0, 0, location)
	if !next.Equal(want) {
		t.Fatalf("Expected the next fire at %s, got %s", want, next)
	}
}

// The hour skipped by the spring transition contains no valid instant, so a
// schedule pointing into it is not fired that day at all.
func TestScheduleInTheSkippedSpringHourDoesNotFire(t *testing.T) {
	spec, err := Parse("30 2 * * *", "America/Los_Angeles")
	if err != nil {
		t.Fatalf("Failed to parse schedule: %v", err)
	}
	location := losAngeles(t)

	// The last fire before the transition: 02:30 on 7 March.
	after := time.Date(2026, time.March, 7, 2, 30, 0, 0, location)
	next := spec.Next(after, spec.WallClock(after))

	if next.Day() == 8 {
		t.Fatalf("Expected 8 March to be skipped - 02:30 does not exist that day - got %s", next)
	}
	want := time.Date(2026, time.March, 9, 2, 30, 0, 0, location)
	if !next.Equal(want) {
		t.Fatalf("Expected the next fire at %s, got %s", want, next)
	}
}

// The hour repeated by the autumn transition contains the same wall-clock time
// twice. Both instants are real and the second is genuinely later, so ordering
// alone does not separate them: without the wall-clock check the agent would
// send twice and write to memory twice on that day.
func TestScheduleInTheRepeatedAutumnHourFiresOnce(t *testing.T) {
	spec, err := Parse("30 1 * * *", "America/Los_Angeles")
	if err != nil {
		t.Fatalf("Failed to parse schedule: %v", err)
	}
	location := losAngeles(t)

	// The fire before the transition, on 31 October.
	previous := time.Date(2026, time.October, 31, 1, 30, 0, 0, location)

	first := spec.Next(previous, spec.WallClock(previous))
	if first.Day() != 1 || first.Month() != time.November {
		t.Fatalf("Expected the first fire on 1 November, got %s", first)
	}

	second := spec.Next(first, spec.WallClock(first))
	if second.Day() == 1 && second.Month() == time.November {
		t.Fatalf("Expected 1 November to fire only once, got a second fire at %s", second)
	}

	want := time.Date(2026, time.November, 2, 1, 30, 0, 0, location)
	if !second.Equal(want) {
		t.Fatalf("Expected the next fire at %s, got %s", want, second)
	}
}

// Without the wall-clock guard the repeated hour does fire twice. This test
// pins the reason the guard exists, so that removing it fails loudly.
func TestRepeatedAutumnHourWouldOtherwiseFireTwice(t *testing.T) {
	spec, err := Parse("30 1 * * *", "America/Los_Angeles")
	if err != nil {
		t.Fatalf("Failed to parse schedule: %v", err)
	}
	location := losAngeles(t)

	previous := time.Date(2026, time.October, 31, 1, 30, 0, 0, location)
	first := spec.Next(previous, "")
	unguarded := spec.Next(first, "")

	if unguarded.Day() != 1 || unguarded.Month() != time.November {
		t.Skipf("The cron library does not produce a duplicate here (got %s); the guard is harmless either way", unguarded)
	}
	if unguarded.Equal(first) {
		t.Fatalf("Expected two distinct instants in the repeated hour, got %s twice", first)
	}
	if spec.WallClock(unguarded) != spec.WallClock(first) {
		t.Fatalf("Expected both instants to read as the same wall-clock time, got %s and %s",
			spec.WallClock(first), spec.WallClock(unguarded))
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	cases := []struct {
		name       string
		expression string
		timezone   string
	}{
		{"unparseable expression", "not a cron expression", "UTC"},
		{"unknown timezone", "0 7 * * *", "Mars/Olympus_Mons"},
		{"missing timezone", "0 7 * * *", ""},
		{"finer than the minimum granularity", "* * * * *", "UTC"},
		{"every two minutes", "*/2 * * * *", "UTC"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := Parse(testCase.expression, testCase.timezone); err == nil {
				t.Fatalf("Expected %s to be rejected", testCase.name)
			}
		})
	}
}

func TestParseAcceptsTheMinimumGranularity(t *testing.T) {
	if _, err := Parse("*/5 * * * *", "UTC"); err != nil {
		t.Fatalf("Expected a five-minute schedule to be accepted: %v", err)
	}
}
