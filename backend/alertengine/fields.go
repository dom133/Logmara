package alertengine

import (
	"encoding/json"
	"regexp"
	"strings"

	"syslytics/model"
)

// matchDevice reports whether fromHostIP is one of the rule's selected
// devices. An empty list matches every device.
func matchDevice(deviceIPs []string, fromHostIP string) bool {
	if len(deviceIPs) == 0 {
		return true
	}
	for _, ip := range deviceIPs {
		if ip == fromHostIP {
			return true
		}
	}
	return false
}

// matchParsers reports whether matchedParsers (the parsers that matched a
// given log entry) intersects the rule's selected parser names. An empty
// list matches any entry, including ones with no matched parser at all.
func matchParsers(parserNames, matchedParsers []string) bool {
	if len(parserNames) == 0 {
		return true
	}
	for _, want := range parserNames {
		for _, got := range matchedParsers {
			if want == got {
				return true
			}
		}
	}
	return false
}

// decodeParsedFields unmarshals an entry's parsed_fields JSON. Returns nil
// (not an error) on absent or malformed data, so callers can treat it the
// same as "no fields available" without extra branching.
func decodeParsedFields(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var fields map[string]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	return fields
}

// matchFieldConditions reports whether conditions match fields, combined
// according to logic: model.FieldConditionsLogicOr requires only one
// condition to match, anything else (including "") defaults to AND
// semantics, requiring every condition to match. An empty condition list
// always matches. A condition whose field is absent from fields never
// matches, regardless of operator.
func matchFieldConditions(conditions []model.AlertFieldCondition, fields map[string]string, logic string) bool {
	if len(conditions) == 0 {
		return true
	}
	if logic == model.FieldConditionsLogicOr {
		for _, cond := range conditions {
			val, ok := fields[cond.FieldName]
			if ok && evaluateFieldCondition(cond, val) {
				return true
			}
		}
		return false
	}
	for _, cond := range conditions {
		val, ok := fields[cond.FieldName]
		if !ok || !evaluateFieldCondition(cond, val) {
			return false
		}
	}
	return true
}

func evaluateFieldCondition(cond model.AlertFieldCondition, value string) bool {
	switch cond.Operator {
	case model.FieldOpContains:
		return strings.Contains(strings.ToLower(value), strings.ToLower(cond.Value))
	case model.FieldOpNotEquals:
		return value != cond.Value
	case model.FieldOpRegex:
		re, err := regexp.Compile(cond.Value)
		if err != nil {
			return false
		}
		return re.MatchString(value)
	case model.FieldOpEquals:
		fallthrough
	default:
		return value == cond.Value
	}
}
