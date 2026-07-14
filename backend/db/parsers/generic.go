package parsers

func init() {
	AllParsers = append(AllParsers, []ParserSeed{
		{
			Name:        "Generic IP Extraction",
			Description: "Generic catch-all for IP addresses in log messages",
			DeviceType:  "generic",
			MatchType:   "all",
			MatchValue:  "",
			Regex:       `(?:src|source|SRC|from)=(\d+\.\d+\.\d+\.\d+).*?(?:dst|dest|DEST|to)=(\d+\.\d+\.\d+\.\d+)`,
			Fields: []FieldSeed{
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
			},
		},
		{
			Name:        "Generic MAC Extraction",
			Description: "Generic catch-all for MAC addresses in log messages",
			DeviceType:  "generic",
			MatchType:   "all",
			MatchValue:  "",
			Regex:       `(?:mac|MAC|ether|hwaddr|client)=([0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2})`,
			Fields: []FieldSeed{
				{Name: "mac_address", Label: "MAC Address", Type: "string"},
			},
		},
	}...)
}