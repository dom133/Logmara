package alertengine

import (
	"testing"

	"syslytics/model"
)

func TestMatchDevice(t *testing.T) {
	cases := []struct {
		deviceIPs []string
		ip        string
		want      bool
	}{
		{nil, "10.0.0.1", true},
		{[]string{}, "10.0.0.1", true},
		{[]string{"10.0.0.1", "10.0.0.2"}, "10.0.0.1", true},
		{[]string{"10.0.0.1", "10.0.0.2"}, "10.0.0.3", false},
	}
	for _, c := range cases {
		if got := matchDevice(c.deviceIPs, c.ip); got != c.want {
			t.Errorf("matchDevice(%v, %q) = %v, want %v", c.deviceIPs, c.ip, got, c.want)
		}
	}
}

func TestMatchParsers(t *testing.T) {
	cases := []struct {
		want, matched []string
		result        bool
	}{
		{nil, []string{"mikrotik"}, true},
		{[]string{}, nil, true},
		{[]string{"mikrotik", "ubiquiti"}, []string{"ubiquiti"}, true},
		{[]string{"mikrotik"}, []string{"ubiquiti"}, false},
		{[]string{"mikrotik"}, nil, false},
	}
	for _, c := range cases {
		if got := matchParsers(c.want, c.matched); got != c.result {
			t.Errorf("matchParsers(%v, %v) = %v, want %v", c.want, c.matched, got, c.result)
		}
	}
}

func TestMatchFieldConditions(t *testing.T) {
	fields := map[string]string{"action": "DENY", "src_ip": "10.0.0.5"}

	cases := []struct {
		name  string
		conds []model.AlertFieldCondition
		logic string
		want  bool
	}{
		{"no conditions", nil, "", true},
		{"equals match", []model.AlertFieldCondition{{FieldName: "action", Operator: model.FieldOpEquals, Value: "DENY"}}, "", true},
		{"equals mismatch", []model.AlertFieldCondition{{FieldName: "action", Operator: model.FieldOpEquals, Value: "ALLOW"}}, "", false},
		{"contains", []model.AlertFieldCondition{{FieldName: "src_ip", Operator: model.FieldOpContains, Value: "10.0.0"}}, "", true},
		{"not_equals", []model.AlertFieldCondition{{FieldName: "action", Operator: model.FieldOpNotEquals, Value: "ALLOW"}}, "", true},
		{"regex", []model.AlertFieldCondition{{FieldName: "src_ip", Operator: model.FieldOpRegex, Value: `^10\.0\.0\.\d+$`}}, "", true},
		{"missing field", []model.AlertFieldCondition{{FieldName: "dst_ip", Operator: model.FieldOpEquals, Value: "1.2.3.4"}}, "", false},
		{"all must match (AND)", []model.AlertFieldCondition{
			{FieldName: "action", Operator: model.FieldOpEquals, Value: "DENY"},
			{FieldName: "src_ip", Operator: model.FieldOpEquals, Value: "wrong"},
		}, "", false},
		{"any must match (OR)", []model.AlertFieldCondition{
			{FieldName: "action", Operator: model.FieldOpEquals, Value: "wrong"},
			{FieldName: "src_ip", Operator: model.FieldOpEquals, Value: "10.0.0.5"},
		}, model.FieldConditionsLogicOr, true},
		{"OR with none matching", []model.AlertFieldCondition{
			{FieldName: "action", Operator: model.FieldOpEquals, Value: "wrong"},
			{FieldName: "src_ip", Operator: model.FieldOpEquals, Value: "wrong"},
		}, model.FieldConditionsLogicOr, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchFieldConditions(c.conds, fields, c.logic); got != c.want {
				t.Errorf("matchFieldConditions(%v, %v, %q) = %v, want %v", c.conds, fields, c.logic, got, c.want)
			}
		})
	}
}

func TestDecodeParsedFields(t *testing.T) {
	if got := decodeParsedFields(nil); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
	if got := decodeParsedFields([]byte("not json")); got != nil {
		t.Errorf("expected nil for malformed input, got %v", got)
	}
	got := decodeParsedFields([]byte(`{"action":"DENY"}`))
	if got["action"] != "DENY" {
		t.Errorf("expected decoded action=DENY, got %v", got)
	}
}
