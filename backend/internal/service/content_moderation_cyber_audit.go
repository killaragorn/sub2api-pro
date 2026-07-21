package service

// cyberPolicyRequestSnapshot preserves the complete inbound request body exactly
// as it was supplied to the gateway. Truncated remains for historical records
// written by earlier versions, but new records are never truncated.
type cyberPolicyRequestSnapshot struct {
	Body          string
	OriginalBytes int64
	StoredBytes   int
	Truncated     bool
}

func buildCyberPolicyRequestSnapshot(_ string, raw []byte) cyberPolicyRequestSnapshot {
	if len(raw) == 0 {
		return cyberPolicyRequestSnapshot{}
	}

	return cyberPolicyRequestSnapshot{
		Body:          string(raw),
		OriginalBytes: int64(len(raw)),
		StoredBytes:   len(raw),
	}
}
