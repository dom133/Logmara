package parsers

func init() {
	AllParsers = append(AllParsers, []ParserSeed{
		{
			Name:        "pfSense Filter Log",
			Description: "Matches pfSense firewall filter log entries",
			DeviceType:  "pfsense",
			MatchType:   "hostname",
			MatchValue:  "pfsense*",
			Regex:       `filter\+.*?(pass|block).*?(\S+)\s+(\d+\.\d+\.\d+\.\d+):\d+\s->\s(\d+\.\d+\.\d+\.\d+):\d+`,
			Fields: []FieldSeed{
				{Name: "action", Label: "Action", Type: "string"},
				{Name: "interface", Label: "Interface", Type: "string"},
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
			},
		},
		{
			Name:        "Suricata Alert",
			Description: "Matches Suricata IDS/IPS alert entries",
			DeviceType:  "pfsense",
			MatchType:   "hostname",
			MatchValue:  "pfsense*",
			Regex:       `\[1:\d+:\d+\]\s+(\S+)\s+(\d+\.\d+\.\d+\.\d+):\d+\s->\s(\d+\.\d+\.\d+\.\d+):\d+`,
			Fields: []FieldSeed{
				{Name: "alert_msg", Label: "Alert Message", Type: "string"},
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
			},
		},
	}...)
}