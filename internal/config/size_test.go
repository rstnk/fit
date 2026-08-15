package config

import "testing"

func TestParseSize(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"10M", 10 << 20, false},
		{"10MB", 10 << 20, false},
		{"10MiB", 10 << 20, false},
		{"10K", 10 << 10, false},
		{"10KB", 10 << 10, false},
		{"1G", 1 << 30, false},
		{"1T", 1 << 40, false},
		{"100", 100, false},
		{"100B", 100, false},
		{"0.5M", (1 << 20) / 2, false},
		{"", 0, true},
		{"M", 0, true},
		{"-5M", 0, true},
		{"0M", 0, true},
		{"5X", 0, true},
		{"5 M", 5 << 20, false}, // internal space before unit is fine
	}
	for _, c := range cases {
		got, err := ParseSize(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseSize(%q) = %d, nil; want an error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSize(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseSize_CaseInsensitive(t *testing.T) {
	got, err := ParseSize("10mib")
	if err != nil || got != 10<<20 {
		t.Errorf("ParseSize(\"10mib\") = %d, %v; want %d, nil", got, err, 10<<20)
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{500, "500 B"},
		{10 << 10, "10.0 KiB"},
		{10 << 20, "10.0 MiB"},
		{2 << 30, "2.00 GiB"},
	}
	for _, c := range cases {
		if got := FormatSize(c.in); got != c.want {
			t.Errorf("FormatSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatBitrate(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		// The reason this is not Mbps everywhere: at one decimal, every audio
		// rate in ordinary use renders as the same "0.1 Mbps".
		{89_000, "89 kbps"},
		{128_000, "128 kbps"},
		{320_000, "320 kbps"},
		{999_999, "999 kbps"},
		{1_000_000, "1.0 Mbps"},
		{2_400_000, "2.4 Mbps"},
	}
	for _, c := range cases {
		if got := FormatBitrate(c.in); got != c.want {
			t.Errorf("FormatBitrate(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatSize_RoundTripsParseSize(t *testing.T) {
	// Not an exact round trip (FormatSize is lossy display), but a sanity
	// check that parsing what we format lands in the same ballpark.
	n, err := ParseSize("25MiB")
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatSize(n); got != "25.0 MiB" {
		t.Errorf("FormatSize(ParseSize(%q)) = %q, want %q", "25MiB", got, "25.0 MiB")
	}
}
