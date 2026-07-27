import { AlertRuleType, FieldConditionOperator, NotificationChannelType } from '../services/api'

export const ruleTypeLabels: Record<AlertRuleType, string> = {
  log_threshold: 'Log threshold',
  device_silence: 'Device silence',
  audit_log: 'Audit log',
  relay_cert_expiring: 'Syslog relay certificate expiring',
  malformed_json: 'Malformed JSON (ingestion)',
}

export const adminOnlyRuleTypes = ['audit_log', 'relay_cert_expiring', 'malformed_json']

export const channelTypeLabels: Record<NotificationChannelType, string> = {
  email: 'Email',
  webhook: 'Webhook',
  slack: 'Slack',
  teams: 'Microsoft Teams',
  in_app: 'In-app',
  push: 'Push (browser)',
}

export const operatorLabels: Record<FieldConditionOperator, string> = {
  equals: 'Equals',
  contains: 'Contains',
  not_equals: 'Not equals',
  regex: 'Regex',
}

export const historyStatusColor: Record<string, string> = {
  sent: 'green',
  partial: 'orange',
  failed: 'red',
  no_channel: 'orange',
}
