package solve

import "testing"

func TestImageSearch_NoCapSingleAttempt(t *testing.T) {
	s := NewImageSearch(ImageConstraints{Quality: 90, Lossy: true})
	att, ok := s.Next()
	if !ok {
		t.Fatal("expected an attempt")
	}
	if att.Quality != 90 || att.Scale != 1 {
		t.Errorf("got %+v, want quality=90 scale=1", att)
	}
	s.Record(999999) // size is irrelevant with no cap
	if _, ok := s.Next(); ok {
		t.Error("expected the search to be done after one attempt with no cap")
	}
}

func TestImageSearch_FirstAttemptIsRequestedQuality(t *testing.T) {
	// An image that already fits at the requested quality should cost
	// exactly one encode.
	s := NewImageSearch(ImageConstraints{Under: 1000, Quality: 90, Lossy: true})
	att, ok := s.Next()
	if !ok || att.Quality != 90 {
		t.Fatalf("first attempt = %+v, ok=%v, want quality=90", att, ok)
	}
	s.Record(500) // fits
	best, haveBest := s.Best()
	if !haveBest || best.Quality != 90 {
		t.Errorf("Best() = %+v, haveBest=%v, want quality=90", best, haveBest)
	}
}

func TestImageSearch_BinarySearchConverges(t *testing.T) {
	// Simulate a monotonic lossy encoder: size scales with quality. The
	// search is bounded to maxProbesPerRound attempts, so it is not
	// guaranteed to find the global best quality in the full 1..100 range;
	// what it must do is return the best of the qualities it actually tried.
	sizeAt := func(q int) int64 { return int64(q) * 100 }
	s := NewImageSearch(ImageConstraints{Under: 8000, Quality: 90, Floor: 1, Lossy: true})

	maxFittingTried := -1
	for {
		att, ok := s.Next()
		if !ok {
			break
		}
		size := sizeAt(att.Quality)
		s.Record(size)
		if size <= 8000 && att.Quality > maxFittingTried {
			maxFittingTried = att.Quality
		}
	}
	best, haveBest := s.Best()
	if !haveBest {
		t.Fatal("expected a best attempt")
	}
	if sizeAt(best.Quality) > 8000 {
		t.Errorf("best quality %d produces %d, over the 8000 cap", best.Quality, sizeAt(best.Quality))
	}
	if best.Quality != maxFittingTried {
		t.Errorf("Best() quality = %d, want %d (the highest fitting quality actually tried)",
			best.Quality, maxFittingTried)
	}
}

func TestImageSearch_MissesFloorThenRescales(t *testing.T) {
	// Every quality at round 0 is over cap; round 1 (rescaled) fits.
	s := NewImageSearch(ImageConstraints{Under: 100, Quality: 90, Floor: 40, Lossy: true})
	rounds := map[int]bool{}
	for {
		att, ok := s.Next()
		if !ok {
			break
		}
		rounds[att.Round] = true
		if att.Round == 0 {
			s.Record(999) // always misses
		} else {
			s.Record(50) // fits once rescaled
		}
	}
	best, haveBest := s.Best()
	if !haveBest {
		t.Fatal("expected a best attempt after rescaling")
	}
	if best.Round == 0 {
		t.Error("best attempt should come from a rescaled round, not round 0")
	}
	if len(rounds) < 2 {
		t.Error("expected the search to have used more than one round")
	}
}

func TestImageSearch_GivesUpAfterMaxRescales(t *testing.T) {
	s := NewImageSearch(ImageConstraints{Under: 1, Quality: 90, Floor: 40, Lossy: true})
	rounds := 0
	for {
		att, ok := s.Next()
		if !ok {
			break
		}
		if att.Round > rounds {
			rounds = att.Round
		}
		s.Record(999999999) // never fits, no matter what
	}
	if _, haveBest := s.Best(); haveBest {
		t.Error("expected no best attempt when nothing ever fit")
	}
	if rounds > MaxRescales {
		t.Errorf("used %d rounds, more than MaxRescales=%d", rounds, MaxRescales)
	}
}

func TestImageSearch_LosslessTriesOneQualityPerRound(t *testing.T) {
	// PNG-like: quality has no effect on size, only scale does. The search
	// must not bisect quality at all: exactly one attempt per round, and
	// every attempt in every round is at qMax.
	s := NewImageSearch(ImageConstraints{Under: 1000, Quality: 90, Lossy: false})

	seenRounds := map[int]int{}
	sizeAtRound := map[int]int64{0: 5000, 1: 3000, 2: 800} // fits at round 2
	for {
		att, ok := s.Next()
		if !ok {
			break
		}
		if att.Quality != 90 {
			t.Errorf("lossless attempt used quality %d, want the configured quality 90 unconditionally", att.Quality)
		}
		seenRounds[att.Round]++
		s.Record(sizeAtRound[att.Round])
	}
	for round, n := range seenRounds {
		if n != 1 {
			t.Errorf("round %d had %d attempts, want exactly 1 for a lossless search", round, n)
		}
	}
	best, ok := s.Best()
	if !ok || best.Round != 2 {
		t.Errorf("Best() = %+v, ok=%v, want round 2", best, ok)
	}
}

func TestImageSearch_LosslessGivesUpAfterMaxRescales(t *testing.T) {
	s := NewImageSearch(ImageConstraints{Under: 1, Quality: 90, Lossy: false})
	attempts := 0
	for {
		_, ok := s.Next()
		if !ok {
			break
		}
		attempts++
		s.Record(999999999)
	}
	if _, haveBest := s.Best(); haveBest {
		t.Error("expected no best attempt")
	}
	if want := MaxRescales + 1; attempts != want {
		t.Errorf("lossless search ran %d attempts, want exactly %d (one per round)", attempts, want)
	}
}

func TestAudioBitrateFor(t *testing.T) {
	// No cap: requested bitrate passes through unchanged.
	if got, err := AudioBitrateFor(128, 0, 0, 60); err != nil || got != 128 {
		t.Errorf("no cap: got %d, %v; want 128, nil", got, err)
	}
	// Generous cap: requested bitrate still passes through, never raised.
	if got, err := AudioBitrateFor(128, 1<<30, 0, 60); err != nil || got != 128 {
		t.Errorf("generous cap: got %d, %v; want 128, nil", got, err)
	}
	// Tight cap forces a lower bitrate, but stays above the 24kbps floor.
	got, err := AudioBitrateFor(128, 800_000, 0, 60) // 800KB over 60s
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got >= 128 {
		t.Errorf("expected a reduced bitrate under a tight cap, got %d", got)
	}
	// Impossibly tight cap errors instead of returning an unusably low bitrate.
	if _, err := AudioBitrateFor(128, 100, 0, 3600); err == nil {
		t.Error("expected an error when the budget is below the 24kbps floor")
	}
}

func TestAudioBitrateForReserved(t *testing.T) {
	// Reserved bytes come off the top: the same cap and duration that left the
	// requested bitrate alone must now cut it, because cover art took the room.
	const under = 2_000_000
	if got, err := AudioBitrateFor(128, under, 0, 60); err != nil || got != 128 {
		t.Fatalf("without reservation: got %d, %v; want 128, nil", got, err)
	}
	got, err := AudioBitrateFor(128, under, 1_500_000, 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got >= 128 {
		t.Errorf("expected a reduced bitrate once 1.5MB is reserved, got %d", got)
	}

	// Art alone larger than the cap is a distinct failure from a budget that
	// merely lands under the bitrate floor, and says so rather than dividing
	// by a negative and reporting nonsense.
	if _, err := AudioBitrateFor(128, 1_000_000, 1_200_000, 60); err == nil {
		t.Error("expected an error when the reservation exceeds the cap")
	}
}
