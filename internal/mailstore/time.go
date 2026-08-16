// Package mailstore reads Apple Mail's Envelope Index database.
package mailstore

import "time"

// The Envelope Index stores date_sent and date_received as REAL values
// holding Unix epoch seconds (seconds since 1970-01-01). Older Mail
// versions stored the Cocoa reference date (2001-01-01) in these columns;
// the V10 schema writes plain Unix epoch seconds.

// EpochToTime converts a Unix epoch timestamp (as stored in the Envelope
// Index) to a UTC time.
func EpochToTime(epoch float64) time.Time {
	sec := int64(epoch)
	nsec := int64((epoch - float64(sec)) * 1e9)
	return time.Unix(sec, nsec).UTC()
}

// TimeToEpoch converts a time to Unix epoch seconds for comparison against
// the Envelope Index date columns.
func TimeToEpoch(t time.Time) float64 {
	return float64(t.Unix())
}
