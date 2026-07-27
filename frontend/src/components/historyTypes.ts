import { NotificationLogEntry, TriggerLogSnapshot, AuditLogRef } from '../services/api'

export interface HistoryGroup {
  key: string
  alertName: string
  alertId?: number
  ruleType: string
  createdAt: string
  triggerLog?: TriggerLogSnapshot
  auditLogRef?: AuditLogRef
  matchedConditions?: string[]
  channels: NotificationLogEntry[]
}

export function groupHistoryEntries(entries: NotificationLogEntry[]): HistoryGroup[] {
  const groups = new Map<string, HistoryGroup>()
  for (const e of entries) {
    const key = e.firing_id || `single-${e.id}`
    const group = groups.get(key)
    if (group) {
      group.channels.push(e)
    } else {
      groups.set(key, {
        key,
        alertName: e.alert_name,
        alertId: e.alert_id,
        ruleType: e.rule_type,
        createdAt: e.created_at,
        triggerLog: e.trigger_log,
        auditLogRef: e.audit_log_ref,
        matchedConditions: e.matched_conditions,
        channels: [e],
      })
    }
  }
  return Array.from(groups.values())
}
