package parsers

func init() {
	AllParsers = append(AllParsers, []ParserSeed{
		{
			Name:        "MikroTik Interface Status",
			Description: "Matches MikroTik interface up/down events",
			DeviceType:  "mikrotik",
			MatchType:   "hostname",
			MatchValue:  "mikrotik*",
			Regex:       `interface\s+(\S+)\s+link\s+(up|down)(?:\s+on\s+the\s+(\S+))?`,
			Fields: []FieldSeed{
				{Name: "interface", Label: "Interface", Type: "string"},
				{Name: "status", Label: "Link Status", Type: "string"},
				{Name: "cause", Label: "Cause", Type: "string"},
			},
		},
		{
			Name:        "MikroTik DHCP Lease",
			Description: "Matches MikroTik DHCP lease events",
			DeviceType:  "mikrotik",
			MatchType:   "hostname",
			MatchValue:  "mikrotik*",
			Regex:       `DHCPLease:(\S+)\s+address=(\d+\.\d+\.\d+\.\d+)\s+mac-address=([0-9A-Fa-f:-]+)`,
			Fields: []FieldSeed{
				{Name: "lease_action", Label: "Lease Action", Type: "string"},
				{Name: "ip_address", Label: "IP Address", Type: "string"},
				{Name: "mac_address", Label: "MAC Address", Type: "string"},
			},
		},
		{
			Name:        "MikroTik User Login",
			Description: "Matches MikroTik user login/logout events",
			DeviceType:  "mikrotik",
			MatchType:   "hostname",
			MatchValue:  "mikrotik*",
			Regex:       `User\s+(\S+)\s+logged\s+(in|out)\s+from\s+(\S+)`,
			Fields: []FieldSeed{
				{Name: "username", Label: "Username", Type: "string"},
				{Name: "login_action", Label: "Action", Type: "string"},
				{Name: "source_ip", Label: "Source IP", Type: "string"},
			},
		},
	}...)
}