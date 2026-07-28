import { TFunction } from 'i18next'
import { AlertRuleType, FieldConditionOperator, NotificationChannelType } from '../services/api'

export function getRuleTypeLabels(t: TFunction): Record<AlertRuleType, string> {
  return {
    log_threshold: t('alertConstants.ruleType.log_threshold'),
    device_silence: t('alertConstants.ruleType.device_silence'),
    audit_log: t('alertConstants.ruleType.audit_log'),
    relay_cert_expiring: t('alertConstants.ruleType.relay_cert_expiring'),
    malformed_json: t('alertConstants.ruleType.malformed_json'),
  }
}

export const adminOnlyRuleTypes = ['audit_log', 'relay_cert_expiring', 'malformed_json']

export function getChannelTypeLabels(t: TFunction): Record<NotificationChannelType, string> {
  return {
    email: t('alertConstants.channelType.email'),
    webhook: t('alertConstants.channelType.webhook'),
    slack: t('alertConstants.channelType.slack'),
    teams: t('alertConstants.channelType.teams'),
    in_app: t('alertConstants.channelType.in_app'),
    push: t('alertConstants.channelType.push'),
  }
}

export function getOperatorLabels(t: TFunction): Record<FieldConditionOperator, string> {
  return {
    equals: t('alertConstants.operator.equals'),
    contains: t('alertConstants.operator.contains'),
    not_equals: t('alertConstants.operator.not_equals'),
    regex: t('alertConstants.operator.regex'),
  }
}

export const historyStatusColor: Record<string, string> = {
  sent: 'green',
  partial: 'orange',
  failed: 'red',
  no_channel: 'orange',
}
