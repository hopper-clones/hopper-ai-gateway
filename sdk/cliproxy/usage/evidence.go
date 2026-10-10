package usage

// TokenEvidence records which native counters were present, independently of
// arithmetic classification. Known marks a parser-owned observation, including
// an empty usage object. Optional detail fields may be absent in a complete
// input/output measurement. Invalid means a supplied counter was not a
// nonnegative integer; a later normalizer must not turn it into measured zero.
type TokenEvidence struct {
	Known      bool
	Input      bool
	Output     bool
	CacheRead  bool
	CacheWrite bool
	Reasoning  bool
	Total      bool
	Invalid    bool
}

// Observed includes invalid supplied evidence, but excludes tier-only metadata
// and empty usage objects.
func (e TokenEvidence) Observed() bool {
	return e.Input || e.Output || e.CacheRead || e.CacheWrite || e.Reasoning || e.Total || e.Invalid
}

// HasTokenMeasurement distinguishes a reported measurement (including zero)
// from metadata. Legacy details retain their existing nonzero interpretation.
func HasTokenMeasurement(d Detail) bool {
	if d.TokenEvidence.Known {
		return d.TokenEvidence.Observed()
	}
	return d.InputTokens != 0 || d.OutputTokens != 0 || d.CachedTokens != 0 ||
		d.CacheReadTokens != 0 || d.CacheCreationTokens != 0 || d.ReasoningTokens != 0 ||
		d.TotalTokens != 0 || d.TokenBreakdown.SchemaVersion != 0
}
