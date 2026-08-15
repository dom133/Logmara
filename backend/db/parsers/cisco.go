package parsers

var CiscoParsers = []ParserSeed{
	{
		Name:        "Cisco IOS Interface",
		Description: "Matches Cisco IOS interface up/down %LINK messages",
		DeviceType:  "cisco",
		MatchType:   "hostname",
		MatchValue:  "cisco*",
		Regex:       `%LINK-(\d+)-(UPDN):\s+Interface\s+(\S+),\s+(change|condition)\s+(is\s+\S+|state\s+\S+)`,
		Fields: []FieldSeed{
			{Name: "msec", Label: "MSEC Code", Type: "string"},
			{Name: "interface", Label: "Interface", Type: "string"},
			{Name: "status", Label: "Status", Type: "string"},
		},
	},
	{
		Name:        "Cisco IOS BGP",
		Description: "Matches Cisco IOS BGP state change messages",
		DeviceType:  "cisco",
		MatchType:   "hostname",
		MatchValue:  "cisco*",
		Regex:       `%BGP-5-ADJCHANGE:\s+Neighbor\s+(\d+\.\d+\.\d+\.\d+)\s+session\s+(Down|Up)`,
		Fields: []FieldSeed{
			{Name: "neighbor_ip", Label: "Neighbor IP", Type: "string"},
			{Name: "session_state", Label: "Session State", Type: "string"},
		},
	},
	{
		Name:        "Cisco IOS Authentication",
		Description: "Matches Cisco authentication success/failure",
		DeviceType:  "cisco",
		MatchType:   "hostname",
		MatchValue:  "cisco*",
		Regex:       `%SEC_LOGIN-\d+-(\S+):\s+User=\S+,\s+Method=(\S+),\s+Reason=(\S+),\s+Info=(\S+)`,
		Fields: []FieldSeed{
			{Name: "auth_result", Label: "Result", Type: "string"},
			{Name: "method", Label: "Method", Type: "string"},
			{Name: "reason", Label: "Reason", Type: "string"},
			{Name: "info", Label: "Info", Type: "string"},
		},
	},
}