package parsers

func init() {
	AllParsers = append(AllParsers, []ParserSeed{
		{
			Name:        "FortiGate Traffic",
			Description: "Matches FortiGate traffic log entries",
			DeviceType:  "fortigate",
			MatchType:   "hostname",
			MatchValue:  "fgt*",
			Regex:       `traffic.*?src=(\d+\.\d+\.\d+\.\d+).*?dst=(\d+\.\d+\.\d+\.\d+).*?srcport=(\d+).*?dstport=(\d+).*?action=(\S+).*?policyid=(\d+).*?sessionid=(\d+).*?proto=(\d+).*?service=(\S+).*?devpriority=(\S+).*?utmstatus=(\S+)`,
			Fields: []FieldSeed{
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
				{Name: "src_port", Label: "Source Port", Type: "string"},
				{Name: "dst_port", Label: "Destination Port", Type: "string"},
				{Name: "action", Label: "Action", Type: "string"},
				{Name: "policy_id", Label: "Policy ID", Type: "string"},
				{Name: "session_id", Label: "Session ID", Type: "string"},
				{Name: "proto", Label: "Protocol", Type: "string"},
				{Name: "service", Label: "Service", Type: "string"},
				{Name: "device_priority", Label: "Priority", Type: "string"},
				{Name: "utm_status", Label: "UTM Status", Type: "string"},
			},
		},
		{
			Name:        "FortiGate Threat",
			Description: "Matches FortiGate threat/security log entries",
			DeviceType:  "fortigate",
			MatchType:   "hostname",
			MatchValue:  "fgt*",
			Regex:       `threat.*?src=(\d+\.\d+\.\d+\.\d+).*?dst=(\d+\.\d+\.\d+\.\d+).*?action=(\S+).*?policyid=(\d+).*?utmstatus=(\S+).*?msg="([^"]*)"`,
			Fields: []FieldSeed{
				{Name: "src_ip", Label: "Source IP", Type: "string"},
				{Name: "dst_ip", Label: "Destination IP", Type: "string"},
				{Name: "action", Label: "Action", Type: "string"},
				{Name: "policy_id", Label: "Policy ID", Type: "string"},
				{Name: "utm_status", Label: "UTM Status", Type: "string"},
				{Name: "msg", Label: "Message", Type: "string"},
			},
		},
	}...)
}