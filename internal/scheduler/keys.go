package scheduler

import (
	"encoding"
	"fmt"
	"strconv"
	"strings"
)

const globalKey = "__global__"

// ResolveKey extracts a value from nested event data using a dot-path.
// Missing or unsupported values fall back to the explicit global bucket.
func ResolveKey(data map[string]any, path string) string {
	if path == "" {
		return globalKey
	}

	parts := strings.Split(path, ".")
	var current any = data

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return globalKey
		}
		current, ok = m[part]
		if !ok {
			return globalKey
		}
	}

	return stringifyKeyValue(current)
}

func stringifyKeyValue(v any) string {
	switch value := v.(type) {
	case nil:
		return globalKey
	case string:
		if value == "" {
			return globalKey
		}
		return value
	case bool:
		return strconv.FormatBool(value)
	case int:
		return strconv.Itoa(value)
	case int8:
		return strconv.FormatInt(int64(value), 10)
	case int16:
		return strconv.FormatInt(int64(value), 10)
	case int32:
		return strconv.FormatInt(int64(value), 10)
	case int64:
		return strconv.FormatInt(value, 10)
	case uint:
		return strconv.FormatUint(uint64(value), 10)
	case uint8:
		return strconv.FormatUint(uint64(value), 10)
	case uint16:
		return strconv.FormatUint(uint64(value), 10)
	case uint32:
		return strconv.FormatUint(uint64(value), 10)
	case uint64:
		return strconv.FormatUint(value, 10)
	case float32:
		return strconv.FormatFloat(float64(value), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case fmt.Stringer:
		s := value.String()
		if s == "" {
			return globalKey
		}
		return s
	case encoding.TextMarshaler:
		b, err := value.MarshalText()
		if err != nil || len(b) == 0 {
			return globalKey
		}
		return string(b)
	default:
		return globalKey
	}
}

func ResolveConcurrencyKeys(rules []ConcurrencyRule, data map[string]any) []string {
	keys := make([]string, len(rules))
	for i, r := range rules {
		keys[i] = ResolveKey(data, r.Key)
	}
	return keys
}

func ResolveRateLimitKeys(rules []RateLimitRule, data map[string]any) []string {
	keys := make([]string, len(rules))
	for i, r := range rules {
		keys[i] = ResolveKey(data, r.Key)
	}
	return keys
}
