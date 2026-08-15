package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSize reads a size written as "10M", "10MB" or "10MiB". Every unit is
// binary, because the platform caps these numbers describe are binary.
func ParseSize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, fmt.Errorf("empty size")
	}
	i := 0
	for i < len(t) && (t[i] >= '0' && t[i] <= '9' || t[i] == '.') {
		i++
	}
	num, unit := t[:i], strings.ToLower(strings.TrimSpace(t[i:]))
	if num == "" {
		return 0, fmt.Errorf("size %q has no number", s)
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("size %q: %w", s, err)
	}

	var mult float64
	switch strings.TrimSuffix(strings.TrimSuffix(unit, "ib"), "b") {
	case "":
		if unit == "b" || unit == "" {
			mult = 1
		} else {
			return 0, fmt.Errorf("size %q has an unknown unit %q", s, unit)
		}
	case "k":
		mult = 1 << 10
	case "m":
		mult = 1 << 20
	case "g":
		mult = 1 << 30
	case "t":
		mult = 1 << 40
	default:
		return 0, fmt.Errorf("size %q has an unknown unit %q", s, unit)
	}
	if v <= 0 {
		return 0, fmt.Errorf("size %q must be positive", s)
	}
	return int64(v * mult), nil
}

// FormatBitrate renders bits per second. Audio sits between 64 and 320 kbps,
// where a Mbps figure at one decimal collapses every rate to the same 0.1, so
// the unit stays kbps until a rate is big enough for Mbps to carry detail.
func FormatBitrate(bps int) string {
	if bps >= 1_000_000 {
		return fmt.Sprintf("%.1f Mbps", float64(bps)/1e6)
	}
	return fmt.Sprintf("%d kbps", bps/1000)
}

// FormatSize renders bytes in binary units, which is what the caps mean.
func FormatSize(b int64) string {
	const unit = 1024
	switch {
	case b < unit:
		return fmt.Sprintf("%d B", b)
	case b < unit*unit:
		return fmt.Sprintf("%.1f KiB", float64(b)/unit)
	case b < unit*unit*unit:
		return fmt.Sprintf("%.1f MiB", float64(b)/(unit*unit))
	default:
		return fmt.Sprintf("%.2f GiB", float64(b)/(unit*unit*unit))
	}
}
