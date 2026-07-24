package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const AccountExtraAffinityConcurrencyReserve = "affinity_concurrency_reserve"

var affinityConcurrencyReserveWarnings sync.Map

// GetAffinityConcurrencyReserve returns the usable protected capacity. Stored
// values are defensively clamped because scheduling must remain safe even when
// legacy or manually edited rows bypass current validation.
func (a *Account) GetAffinityConcurrencyReserve() int {
	if a == nil || a.Platform != PlatformOpenAI || a.Extra == nil {
		return 0
	}
	raw, exists := a.Extra[AccountExtraAffinityConcurrencyReserve]
	if !exists || raw == nil {
		return 0
	}
	reserve, ok := affinityConcurrencyReserveValue(raw)
	if !ok {
		warnAffinityConcurrencyReserveOnce(a, raw, "invalid_value", 0)
		return 0
	}
	if a.Concurrency <= 0 {
		if reserve != 0 {
			warnAffinityConcurrencyReserveOnce(a, raw, "ignored_for_unlimited_concurrency", 0)
		}
		return 0
	}
	if reserve < 0 {
		warnAffinityConcurrencyReserveOnce(a, raw, "negative_value", 0)
		return 0
	}
	if reserve == 0 {
		return 0
	}
	if reserve >= a.Concurrency {
		clamped := a.Concurrency - 1
		warnAffinityConcurrencyReserveOnce(a, raw, "clamped_to_preserve_general_capacity", clamped)
		return clamped
	}
	return reserve
}

func warnAffinityConcurrencyReserveOnce(a *Account, raw any, reason string, effectiveReserve int) {
	if a == nil {
		return
	}
	configuredReserve, numeric := affinityConcurrencyReserveValue(raw)
	configuredSignature := fmt.Sprintf("type=%T", raw)
	if numeric {
		configuredSignature = strconv.Itoa(configuredReserve)
	}
	signature := fmt.Sprintf("%d:%s:%s", a.Concurrency, reason, configuredSignature)
	if !storeAccountWarningSignature(&affinityConcurrencyReserveWarnings, a.ID, signature) {
		return
	}

	attrs := []any{
		"account_id", a.ID,
		"concurrency", a.Concurrency,
		"effective_reserve", effectiveReserve,
		"reason", reason,
	}
	if numeric {
		attrs = append(attrs, "configured_reserve", configuredReserve)
	} else {
		attrs = append(attrs, "configured_reserve_type", fmt.Sprintf("%T", raw))
	}
	slog.Warn("invalid OpenAI affinity concurrency reserve adjusted at runtime", attrs...)
}

func storeAccountWarningSignature(warnings *sync.Map, accountID int64, signature string) bool {
	if warnings == nil || accountID <= 0 {
		return false
	}
	for {
		previous, loaded := warnings.LoadOrStore(accountID, signature)
		if !loaded {
			return true
		}
		if previous == signature {
			return false
		}
		if warnings.CompareAndSwap(accountID, previous, signature) {
			return true
		}
	}
}

// GeneralConcurrencyLimit is the admission limit for new sessions and
// temporary overflow requests. Non-positive concurrency retains the existing
// unlimited convention.
func (a *Account) GeneralConcurrencyLimit() int {
	if a == nil {
		return 0
	}
	if a.Concurrency <= 0 {
		return a.Concurrency
	}
	return a.Concurrency - a.GetAffinityConcurrencyReserve()
}

// ConcurrencyLimitForAffinity applies the reserve contract consistently across
// every OpenAI scheduler. Affinity traffic may use the full account capacity;
// new sessions and temporary overflow may use only the general partition.
func (a *Account) ConcurrencyLimitForAffinity(affinity bool) int {
	if a == nil {
		return 0
	}
	if affinity {
		return a.Concurrency
	}
	return a.GeneralConcurrencyLimit()
}

func ValidateAccountAffinityConcurrencyReserve(platform string, concurrency int, extra map[string]any) error {
	raw, exists := extra[AccountExtraAffinityConcurrencyReserve]
	if !exists || raw == nil {
		return nil
	}
	reserve, ok := affinityConcurrencyReserveValue(raw)
	if !ok {
		return infraerrors.BadRequest("INVALID_AFFINITY_CONCURRENCY_RESERVE", "affinity_concurrency_reserve must be a non-negative integer")
	}
	if reserve < 0 {
		return infraerrors.BadRequest("INVALID_AFFINITY_CONCURRENCY_RESERVE", "affinity_concurrency_reserve must be a non-negative integer")
	}
	if platform != PlatformOpenAI {
		if reserve != 0 {
			return infraerrors.BadRequest("INVALID_AFFINITY_CONCURRENCY_RESERVE", "affinity_concurrency_reserve is only supported for OpenAI accounts")
		}
		return nil
	}
	if concurrency <= 0 {
		if reserve != 0 {
			return infraerrors.BadRequest("INVALID_AFFINITY_CONCURRENCY_RESERVE", "affinity_concurrency_reserve must be 0 when concurrency is unlimited")
		}
		return nil
	}
	if reserve >= concurrency {
		return infraerrors.BadRequest("INVALID_AFFINITY_CONCURRENCY_RESERVE", "affinity_concurrency_reserve must be less than concurrency")
	}
	return nil
}

func affinityConcurrencyReserveValue(value any) (int, bool) {
	maxInt := uint64(^uint(0) >> 1)
	switch v := value.(type) {
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		if int64(int(v)) != v {
			return 0, false
		}
		return int(v), true
	case uint8:
		return int(v), true
	case uint16:
		return int(v), true
	case uint32:
		if uint64(v) > maxInt {
			return 0, false
		}
		return int(v), true
	case uint:
		if uint64(v) > maxInt {
			return 0, false
		}
		return int(v), true
	case uint64:
		if v > maxInt {
			return 0, false
		}
		return int(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v || v > float64(math.MaxInt) || v < float64(math.MinInt) {
			return 0, false
		}
		return int(v), true
	case float32:
		return affinityConcurrencyReserveValue(float64(v))
	case json.Number:
		n, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil || int64(int(n)) != n {
			return 0, false
		}
		return int(n), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || int64(int(n)) != n {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}
