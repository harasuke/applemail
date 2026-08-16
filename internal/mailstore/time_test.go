package mailstore

import (
	"testing"
	"time"
)

func TestEpochToTime(t *testing.T) {
	tests := []struct {
		name  string
		epoch float64
		want  time.Time
	}{
		{"unix epoch zero is 1970-01-01", 0, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"one hour after unix epoch", 3600, time.Date(1970, 1, 1, 1, 0, 0, 0, time.UTC)},
		{"a real message timestamp", 1785960682, time.Date(2026, 8, 5, 20, 11, 22, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EpochToTime(tt.epoch)
			if !got.Equal(tt.want) {
				t.Errorf("EpochToTime(%v) = %v, want %v", tt.epoch, got, tt.want)
			}
		})
	}
}

func TestTimeToEpochRoundTrip(t *testing.T) {
	original := time.Date(2026, 8, 16, 12, 30, 0, 0, time.UTC)
	got := EpochToTime(TimeToEpoch(original))
	if !got.Equal(original) {
		t.Errorf("round trip = %v, want %v", got, original)
	}
}
