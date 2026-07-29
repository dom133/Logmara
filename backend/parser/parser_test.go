package parser

import (
	"testing"

	dbparsers "logmara/db/parsers"
	"logmara/model"
)

func strPtr(s string) *string { return &s }

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		want    bool
	}{
		{"ubnt*", "ubnt-ap1", true},
		{"ubnt*", "cisco-sw1", false},
		{"*.example.com", "host1.example.com", true},
		{"*.example.com", "example.com", false},
		{"host?", "host1", true},
		{"host?", "host12", false},
	}

	for _, tt := range tests {
		got := matchGlob(tt.pattern, tt.value)
		if got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.want)
		}
	}
}

func TestEngineMatchByHostname(t *testing.T) {
	e := &Engine{
		parsers: []model.Parser{
			{
				Name:       "hostname parser",
				MatchType:  "hostname",
				MatchValue: strPtr("ubnt*"),
				Regex:      `AP\s+(\S+)\s+connected`,
				Fields:     []model.ParsedField{{Name: "mac"}},
			},
			{
				Name:       "other hostname",
				MatchType:  "hostname",
				MatchValue: strPtr("cisco*"),
				Regex:      `AP\s+(\S+)\s+connected`,
			},
		},
	}

	matched := e.Match("ubnt-ap1", "", "AP aa:bb connected")
	if len(matched) != 1 || matched[0].Name != "hostname parser" {
		t.Fatalf("expected only hostname parser to match, got %+v", matched)
	}

	matched = e.Match("cisco-sw1", "", "unrelated message")
	if len(matched) != 0 {
		t.Fatalf("expected no match when hostname matches but message doesn't, got %+v", matched)
	}
}

func TestEngineMatchByMessageContains(t *testing.T) {
	e := &Engine{
		parsers: []model.Parser{
			{
				Name:       "firewall parser",
				MatchType:  "message",
				MatchValue: strPtr("DESCR="),
				Regex:      `DESCR="([^"]+)"`,
				Fields:     []model.ParsedField{{Name: "description"}},
			},
		},
	}

	matched := e.Match("any-host", "", `[block] DESCR="test rule" SRC=1.2.3.4`)
	if len(matched) != 1 {
		t.Fatalf("expected message parser to match, got %+v", matched)
	}

	fields := e.Extract(&matched[0], `[block] DESCR="test rule" SRC=1.2.3.4`)
	if fields["description"] != "test rule" {
		t.Errorf("description = %q, want %q", fields["description"], "test rule")
	}
}

func TestEngineParseMergesFieldsAcrossParsers(t *testing.T) {
	e := &Engine{
		parsers: []model.Parser{
			{
				Name:      "parser A",
				MatchType: "all",
				Regex:     `foo=(\S+)`,
				Fields:    []model.ParsedField{{Name: "foo"}},
			},
			{
				Name:      "parser B",
				MatchType: "all",
				Regex:     `bar=(\S+)`,
				Fields:    []model.ParsedField{{Name: "bar"}},
			},
		},
	}

	result := e.Parse("host", "app", "foo=1 bar=2")
	if result == nil {
		t.Fatal("expected a parse result, got nil")
	}
	if result.Fields["foo"] != "1" || result.Fields["bar"] != "2" {
		t.Errorf("fields = %+v, want foo=1 bar=2", result.Fields)
	}
	if len(result.Parsers) != 2 {
		t.Errorf("parsers = %v, want 2 entries", result.Parsers)
	}
}

func TestEngineParseNoMatch(t *testing.T) {
	e := &Engine{
		parsers: []model.Parser{
			{Name: "never", MatchType: "message", MatchValue: strPtr("NOPE"), Regex: `.*`},
		},
	}

	if result := e.Parse("host", "app", "unrelated message"); result != nil {
		t.Errorf("expected nil result for non-matching message, got %+v", result)
	}
}

func TestEngineTestParser(t *testing.T) {
	e := &Engine{}

	resp, err := e.TestParser(`user=(\S+) action=(\S+)`, "user=alice action=login")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Matched {
		t.Fatal("expected pattern to match sample log")
	}
	if resp.Fields["group_0"] != "alice" || resp.Fields["group_1"] != "login" {
		t.Errorf("fields = %+v", resp.Fields)
	}

	resp, err = e.TestParser(`(`, "anything")
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
	if resp.Matched {
		t.Error("expected Matched=false for invalid regex")
	}
}

// TestUbiquitiWANFailoverParser is a regression test for the "UniFi Network
// WAN Failover" builtin parser (signature 105): it loads the real embedded
// seed definition and checks it extracts every field from an actual sample
// log line, so a future edit to the regex or field list can't silently drop
// a capture group without a test catching it.
func TestUbiquitiWANFailoverParser(t *testing.T) {
	seeds, errs := dbparsers.LoadAll("")
	for _, err := range errs {
		t.Fatalf("LoadAll error: %v", err)
	}

	var seed *dbparsers.ParserSeed
	for i := range seeds {
		if seeds[i].Name == "UniFi Network WAN Failover" {
			seed = &seeds[i]
			break
		}
	}
	if seed == nil {
		t.Fatal("builtin parser \"UniFi Network WAN Failover\" not found in embedded defaults")
	}

	fields := make([]model.ParsedField, len(seed.Fields))
	for i, f := range seed.Fields {
		fields[i] = model.ParsedField{Name: f.Name}
	}
	parser := model.Parser{Name: seed.Name, Regex: seed.Regex, Fields: fields}

	line := `0|Ubiquiti|UniFi Network|10.4.57|105|Temporary Internet Failover|6|UNIFIcategory=Internet and WAN UNIFIhost=DMP Gdynia UNIFIdeviceMac=d8:b3:70:46:44:71 UNIFIdeviceName=DMP Gdynia UNIFIdeviceModel=UDM-Pro UNIFIdeviceIp=10.1.0.1 UNIFIdeviceVersion=5.1.19 UNIFIwanName=Internet 1 UNIFIwanId=WAN1 UNIFIwanPort=9 UNIFIwanIsp=T-Mobile Polska UNIFIwanSubnet=10.10.197.65/30 UNIFIwanSla=Auto UNIFIfailoverWanName=Internet 2 UNIFIfailoverWanId=WAN2 UNIFIfailoverWanPort=8 UNIFIfailoverWanIsp=Plus Poland UNIFIfailoverWanSubnet=10.155.243.71/28 UNIFIreportedDuration=42s UNIFIutcTime=2026-07-26T19:57:26.096Z msg=Internet connection WAN1 (T-Mobile Polska) on port 9 is restored after temporarily failing over to WAN2 (Plus Poland).`

	e := &Engine{}
	extracted := e.Extract(&parser, line)
	if extracted == nil {
		t.Fatal("expected regex to match sample WAN failover line")
	}

	want := map[string]string{
		"category":          "Internet and WAN",
		"unifi_host":        "DMP Gdynia",
		"device_mac":        "d8:b3:70:46:44:71",
		"wan_id":            "WAN1",
		"wan_isp":           "T-Mobile Polska",
		"failover_wan_id":   "WAN2",
		"failover_wan_isp":  "Plus Poland",
		"reported_duration": "42s",
	}
	for k, v := range want {
		if extracted[k] != v {
			t.Errorf("field %q = %q, want %q", k, extracted[k], v)
		}
	}
	if len(extracted) != len(fields) {
		t.Errorf("extracted %d fields, want %d (regex capture groups vs field list mismatch)", len(extracted), len(fields))
	}
}

// TestWindowsEventLogJSONParsers is a regression test for the two builtin
// "windows" device_type parsers (see defaults/windows.json): the generic
// envelope parser that extracts eventId/providerName/etc. from any
// Windows Event Log forwarded as JSON, and the more specific RDP session
// reconnect parser that additionally pulls user/session/source IP out of a
// Microsoft-Windows-TerminalServices-LocalSessionManager event's message
// text. It loads the real embedded seed definitions and checks each against
// an actual sample log line so a future regex/field edit can't silently drop
// a capture group without a test catching it.
func TestWindowsEventLogJSONParsers(t *testing.T) {
	seeds, errs := dbparsers.LoadAll("")
	for _, err := range errs {
		t.Fatalf("LoadAll error: %v", err)
	}

	findSeed := func(name string) *dbparsers.ParserSeed {
		for i := range seeds {
			if seeds[i].Name == name {
				return &seeds[i]
			}
		}
		t.Fatalf("builtin parser %q not found in embedded defaults", name)
		return nil
	}

	toModelParser := func(seed *dbparsers.ParserSeed) model.Parser {
		fields := make([]model.ParsedField, len(seed.Fields))
		for i, f := range seed.Fields {
			fields[i] = model.ParsedField{Name: f.Name}
		}
		return model.Parser{Name: seed.Name, Regex: seed.Regex, Fields: fields}
	}

	e := &Engine{}

	t.Run("envelope", func(t *testing.T) {
		seed := findSeed("Windows Event Log (JSON)")
		parser := toModelParser(seed)

		line := `{"message":"Zatwierdzono pamięć podręczną programu rozpoznawania aplikacji.","eventId":28019,"providerName":"Microsoft-Windows-Shell-Core","level":"Informational","timeCreated":"2026-07-29T19:54:51.4646243+02:00","logName":"Microsoft-Windows-Shell-Core/Operational","machineName":"AD.dom133.local","userId":"S-1-5-21-3699043479-1619627317-4020929063-1118","activityId":"-"}`

		extracted := e.Extract(&parser, line)
		if extracted == nil {
			t.Fatal("expected regex to match sample Shell-Core event")
		}

		want := map[string]string{
			"event_id":      "28019",
			"provider_name": "Microsoft-Windows-Shell-Core",
			"level":         "Informational",
			"log_name":      "Microsoft-Windows-Shell-Core/Operational",
			"machine_name":  "AD.dom133.local",
			"activity_id":   "-",
		}
		for k, v := range want {
			if extracted[k] != v {
				t.Errorf("field %q = %q, want %q", k, extracted[k], v)
			}
		}
		if len(extracted) != len(seed.Fields) {
			t.Errorf("extracted %d fields, want %d (regex capture groups vs field list mismatch)", len(extracted), len(seed.Fields))
		}
	})

	t.Run("rdp reconnect", func(t *testing.T) {
		envelopeSeed := findSeed("Windows Event Log (JSON)")
		rdpSeed := findSeed("Windows RDP Session Reconnect")

		line := `{"message":"Usługi pulpitu zdalnego: ponowne nawiązanie połączenia sesji powiodło się:\\r\\n\\r\\nUżytkownik: DOM133\\\\dkr\\r\\nIdentyfikator sesji: 2\\r\\nŹródłowy adres sieciowy: 10.1.10.253","eventId":25,"providerName":"Microsoft-Windows-TerminalServices-LocalSessionManager","level":"Informational","timeCreated":"2026-07-29T19:40:59.1914177+02:00","logName":"Microsoft-Windows-TerminalServices-LocalSessionManager/Operational","machineName":"AD.dom133.local","userId":"S-1-5-18","activityId":"f420ec05-775d-4e0f-974e-ff54e0c40000"}`

		// The envelope parser must still match this provider (it's just a
		// generic "any Microsoft-Windows- provider" match), and the RDP
		// parser must additionally match/extract on top of it.
		envelopeRe, err := compileCached(envelopeSeed.Regex)
		if err != nil {
			t.Fatalf("envelope regex compile error: %v", err)
		}
		if !envelopeRe.MatchString(line) {
			t.Error("envelope parser regex did not match RDP reconnect line")
		}

		rdpParser := toModelParser(rdpSeed)
		extracted := e.Extract(&rdpParser, line)
		if extracted == nil {
			t.Fatal("expected RDP regex to match sample reconnect line")
		}

		want := map[string]string{
			"rdp_user":       `DOM133\\\\dkr`,
			"rdp_session_id": "2",
			"src_ip":         "10.1.10.253",
		}
		for k, v := range want {
			if extracted[k] != v {
				t.Errorf("field %q = %q, want %q", k, extracted[k], v)
			}
		}
	})
}
