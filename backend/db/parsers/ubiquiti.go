package parsers

func init() {
	AllParsers = append(AllParsers, []ParserSeed{
		{
			Name:        "Ubiquiti AP Event",
			Description: "Matches Ubiquiti UniFi AP connect/disconnect/reboot events",
			DeviceType:  "ubiquiti",
			MatchType:   "hostname",
			MatchValue:  "ubnt*",
			Regex:       `AP\s+([0-9A-Fa-f:]+)\s+(\S+)\s+on\s+(\S+)`,
			Fields: []FieldSeed{
				{Name: "mac_address", Label: "MAC Address", Type: "string"},
				{Name: "event_type", Label: "Event Type", Type: "string"},
				{Name: "site", Label: "Site", Type: "string"},
			},
		},
		{
			Name:        "Ubiquiti Client Connect",
			Description: "Matches Ubiquiti client association events",
			DeviceType:  "ubiquiti",
			MatchType:   "hostname",
			MatchValue:  "ubnt*",
			Regex:       `client\s+([0-9A-Fa-f:]+)\s+(\S+)\s+on\s+(\S+)\s+channel\s+(\d+)$`,
			Fields: []FieldSeed{
				{Name: "client_mac", Label: "Client MAC", Type: "string"},
				{Name: "action", Label: "Action", Type: "string"},
				{Name: "ssid", Label: "SSID", Type: "string"},
				{Name: "channel", Label: "Channel", Type: "string"},
			},
		},
	}...)
}