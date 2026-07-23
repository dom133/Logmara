package parsers

func init() {
	AllParsers = append(AllParsers, []ParserSeed{
		{
			Name:        "Palo Alto Threat",
			Description: "Matches Palo Alto threat log entries",
			DeviceType:  "palo_alto",
			MatchType:   "hostname",
			MatchValue:  "pan*",
			Regex:       `threat.*?src=(\d+\.\d+\.\d+\.\d+).*?dst=(\d+\.\d+\.\d+\.\d+).*?action=(\S+).*?category=(\S+)`,
			Fields: []FieldSeed{
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
				{Name: "action", Label: "Action", Type: "string"},
				{Name: "category", Label: "Category", Type: "string"},
			},
		},
		{
			Name:        "Palo Alto Traffic",
			Description: "Matches Palo Alto traffic log entries",
			DeviceType:  "palo_alto",
			MatchType:   "hostname",
			MatchValue:  "pan*",
			Regex:       `traffic.*?src=(\d+\.\d+\.\d+\.\d+).*?dst=(\d+\.\d+\.\d+\.\d+).*?proto=(\S+).*?action=(\S+)`,
			Fields: []FieldSeed{
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
				{Name: "protocol", Label: "Protocol", Type: "string"},
				{Name: "action", Label: "Action", Type: "string"},
			},
		},
	}...)
}
