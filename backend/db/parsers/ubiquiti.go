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
		{
			Name:        "Ubiquiti Firewall Log",
			Description: "Matches Ubiquiti EdgeRouter/Dream Machine iptables-style firewall logs",
			DeviceType:  "ubiquiti",
			MatchType:   "message",
			MatchValue:  "DESCR=",
			Regex:       `\[(\S+)\]\s+DESCR="([^"]+)"\s+IN=(\S*)\s+OUT=(\S*)\s+MAC=([0-9A-Fa-f:]+)\s+SRC=(\d+\.\d+\.\d+\.\d+)\s+DST=(\d+\.\d+\.\d+\.\d+)\s+.*?LEN=(\d+)\s+.*?PROTO=(\S+)(?:\s+SPT=(\d+))?(?:\s+DPT=(\d+))?`,
			Fields: []FieldSeed{
				{Name: "rule_id", Label: "Rule ID", Type: "string"},
				{Name: "description", Label: "Description", Type: "string"},
				{Name: "in_iface", Label: "In Interface", Type: "string"},
				{Name: "out_iface", Label: "Out Interface", Type: "string"},
				{Name: "mac_address", Label: "MAC Address", Type: "string"},
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Dest IP", Type: "string"},
				{Name: "packet_len", Label: "Packet Length", Type: "string"},
				{Name: "proto", Label: "Protocol", Type: "string"},
				{Name: "src_port", Label: "Source Port", Type: "string"},
				{Name: "dst_port", Label: "Dest Port", Type: "string"},
			},
		},
	}...)
}