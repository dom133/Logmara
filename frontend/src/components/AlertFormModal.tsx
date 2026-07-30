import { Form, Input, InputNumber, Select, Switch, Space, Modal, Button } from 'antd'
import { useTranslation } from 'react-i18next'
import { Alert, AlertRequest, AlertRuleType, DeviceStats, Parser, ParsedField, NotificationChannel, resolveDeviceDisplayName } from '../services/api'
import { getRuleTypeLabels, adminOnlyRuleTypes, getOperatorLabels, getChannelTypeLabels } from '../constants/alertConstants'
import { getSeverityLabels } from '../constants'

interface AlertFormModalProps {
  open: boolean
  editing: Alert | null
  isAdmin: boolean
  devices: DeviceStats[]
  parsers: Parser[]
  parsedFields: ParsedField[]
  channels: NotificationChannel[]
  form: ReturnType<typeof Form.useForm>[0]
  onCancel: () => void
  onOk: () => void
}

export default function AlertFormModal({ open, editing, isAdmin, devices, parsers, parsedFields, channels, form, onCancel, onOk }: AlertFormModalProps) {
  const { t } = useTranslation()
  const ruleType = Form.useWatch('rule_type', form)
  const fireOnEveryMatch = Form.useWatch('fire_on_every_match', form)
  const selectedParsers: string[] = Form.useWatch('parser_names', form) || []
  const selectedDevices: string[] = Form.useWatch('device_ips', form) || []
  const fieldConditions: any[] = Form.useWatch('field_conditions', form) || []

  const deviceOptions = devices.map(d => ({ label: resolveDeviceDisplayName(d), value: d.fromhost_ip }))

  const availableParserNames = selectedDevices.length === 0
    ? null
    : new Set(devices.filter(d => selectedDevices.includes(d.fromhost_ip)).flatMap(d => d.matched_parsers || []))
  const parserOptions = parsers
    .filter(p => availableParserNames === null || availableParserNames.has(p.name))
    .map(p => ({ label: p.name, value: p.name }))
  const fieldOptions = Array.from(
    new Map(
      parsedFields
        .filter(f => selectedParsers.length === 0 || selectedParsers.includes(f.parser_name))
        .map(f => [f.field_name, f]),
    ).values(),
  ).map(f => ({ label: `${f.field_label || f.field_name} (${f.parser_name})`, value: f.field_name }))

  return (
    <Modal
      title={editing ? t('alerts.editRule') : t('alerts.newRule')}
      open={open}
      onCancel={onCancel}
      onOk={onOk}
      width={{ sm: '90%', md: 640 }}
    >
      <Form form={form} layout="vertical">
        <Form.Item name="name" label={t('common.name')} rules={[{ required: true }]}>
          <Input />
        </Form.Item>
        <Form.Item name="description" label={t('common.description')}>
          <Input.TextArea rows={2} />
        </Form.Item>
        <Form.Item name="rule_type" label={t('alerts.ruleType')} rules={[{ required: true }]}>
          <Select
            options={Object.entries(getRuleTypeLabels(t))
              .filter(([key]) => isAdmin || !adminOnlyRuleTypes.includes(key))
              .map(([value, label]) => ({ value, label }))}
          />
        </Form.Item>

        {(ruleType === 'log_threshold' || ruleType === 'device_silence') && (
          <Form.Item name="device_ips" label={t('alerts.devicesLabel')} tooltip={t('alerts.watchAllDevicesTooltip')}>
            <Select mode="multiple" allowClear placeholder={t('alerts.allDevicesPlaceholder')} options={deviceOptions} />
          </Form.Item>
        )}

        {ruleType === 'log_threshold' && (
          <>
            <Form.Item name="severity" label={t('alerts.minimumSeverity')} tooltip={t('alerts.anySeverityTooltip')}>
              <Select allowClear options={['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug'].map(s => ({ value: s, label: getSeverityLabels(t)[s] }))} />
            </Form.Item>
            <Form.Item name="parser_names" label={t('parsers.title')} tooltip={t('alerts.parsersConditionTooltip')}>
              <Select mode="multiple" allowClear placeholder={t('alerts.anyParserPlaceholder')} options={parserOptions} />
            </Form.Item>
            <Form.Item name="message_pattern" label={t('alerts.messagePattern')} tooltip={t('alerts.messagePatternTooltip')}>
              <Input placeholder={t('alerts.messagePatternPlaceholder')} />
            </Form.Item>

            {ruleType === 'log_threshold' && (
              <>
                <Form.Item name="field_conditions_logic" label={t('alerts.combineConditionsWith')} hidden={fieldConditions.length <= 1} style={{ maxWidth: 220, marginBottom: 8 }}>
                  <Select options={[
                    { value: 'and', label: t('alerts.conditionAnd') },
                    { value: 'or', label: t('alerts.conditionOr') },
                  ]} />
                </Form.Item>
                <Form.List name="field_conditions">
                  {(fields, { add, remove }) => (
                    <>
                      {fields.map((field) => (
                        <Space key={field.key} align="baseline" style={{ display: 'flex', marginBottom: 8 }} wrap>
                          <Form.Item name={[field.name, 'field_name']} rules={[{ required: true, message: t('alerts.fieldRequired') }]} noStyle>
                            <Select showSearch placeholder={t('alerts.fieldPlaceholder')} style={{ width: 200 }} options={fieldOptions} />
                          </Form.Item>
                          <Form.Item name={[field.name, 'operator']} initialValue="equals" noStyle>
                            <Select style={{ width: 130 }} options={Object.entries(getOperatorLabels(t)).map(([value, label]) => ({ value, label }))} />
                          </Form.Item>
                          <Form.Item name={[field.name, 'value']} rules={[{ required: true, message: t('alerts.valueRequired') }]} noStyle>
                            <Input placeholder={t('parsers.value')} style={{ width: 180 }} />
                          </Form.Item>
                          <Button type="link" danger onClick={() => remove(field.name)}>{t('common.remove')}</Button>
                        </Space>
                      ))}
                      <Button type="dashed" onClick={() => add({ operator: 'equals' })} block>{t('alerts.addFieldCondition')}</Button>
                    </>
                  )}
                </Form.List>
              </>
            )}

            <Form.Item name="fire_on_every_match" label={t('alerts.fireOnEveryMatch')} valuePropName="checked" tooltip={t('alerts.fireOnEveryMatchTooltip')}>
              <Switch />
            </Form.Item>

            {!fireOnEveryMatch && (
              <Space.Compact block>
                <Form.Item name="threshold" label={t('alerts.thresholdMatches')} style={{ flex: 1 }} rules={[{ required: true }]}>
                  <InputNumber min={1} style={{ width: '100%' }} />
                </Form.Item>
                <Form.Item name="window_minutes" label={t('alerts.windowMinutes')} style={{ flex: 1 }} rules={[{ required: true }]}>
                  <InputNumber min={1} style={{ width: '100%' }} />
                </Form.Item>
              </Space.Compact>
            )}
          </>
        )}

        {ruleType === 'device_silence' && (
          <Form.Item name="threshold" label={t('alerts.silentAfterMinutes')} rules={[{ required: true }]}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
        )}

        {ruleType === 'audit_log' && (
          <Form.Item name="audit_action_filter" label={t('alerts.actionFilter')} tooltip={t('alerts.anyAuditActionTooltip')}>
            <Select placeholder={t('alerts.selectAuditAction')} allowClear>
              <Select.OptGroup label={t('alerts.groupAuthentication')}>
                <Select.Option value="login_success">login_success</Select.Option>
                <Select.Option value="login_failed">login_failed</Select.Option>
                <Select.Option value="login_failed_lockout">login_failed_lockout</Select.Option>
                <Select.Option value="login_failed_inactive">login_failed_inactive</Select.Option>
                <Select.Option value="refresh_success">refresh_success</Select.Option>
                <Select.Option value="refresh_failed">refresh_failed</Select.Option>
                <Select.Option value="refresh_race_recovered">refresh_race_recovered</Select.Option>
                <Select.Option value="password_changed">password_changed</Select.Option>
              </Select.OptGroup>
              <Select.OptGroup label={t('alerts.groupUserManagement')}>
                <Select.Option value="user_created">user_created</Select.Option>
                <Select.Option value="user_updated">user_updated</Select.Option>
                <Select.Option value="user_deleted">user_deleted</Select.Option>
                <Select.Option value="password_reset_by_admin">password_reset_by_admin</Select.Option>
                <Select.Option value="user_unlocked">user_unlocked</Select.Option>
              </Select.OptGroup>
              <Select.OptGroup label={t('admin.settings')}>
                <Select.Option value="settings_updated">settings_updated</Select.Option>
                <Select.Option value="logs_purged">logs_purged</Select.Option>
              </Select.OptGroup>
              <Select.OptGroup label={t('alerts.groupSslTls')}>
                <Select.Option value="ssl_cert_uploaded">ssl_cert_uploaded</Select.Option>
              </Select.OptGroup>
              <Select.OptGroup label={t('alerts.groupRelay')}>
                <Select.Option value="relay_certificate_issued">relay_certificate_issued</Select.Option>
                <Select.Option value="relay_certificate_revoked">relay_certificate_revoked</Select.Option>
                <Select.Option value="relay_whitelist_added">relay_whitelist_added</Select.Option>
                <Select.Option value="relay_whitelist_removed">relay_whitelist_removed</Select.Option>
              </Select.OptGroup>
              <Select.OptGroup label={t('alerts.groupSystem')}>
                <Select.Option value="slow_query">slow_query</Select.Option>
                <Select.Option value="bulk_operation">bulk_operation</Select.Option>
              </Select.OptGroup>
            </Select>
          </Form.Item>
        )}

        {ruleType === 'relay_cert_expiring' && (
          <Form.Item name="threshold" label={t('alerts.warnBeforeExpiryDays')} tooltip={t('alerts.certExpiryTooltip')} rules={[{ required: true }]}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
        )}

        {ruleType === 'malformed_json' && (
          <Form.Item name="fire_on_every_match" label={t('alerts.fireOnEveryMatch')} valuePropName="checked" tooltip={t('alerts.fireOnEveryMatchMalformedTooltip')}>
            <Switch />
          </Form.Item>
        )}

        {!((ruleType === 'log_threshold' || ruleType === 'malformed_json') && fireOnEveryMatch) && (
          <Form.Item name="cooldown_minutes" label={t('alerts.cooldownMinutes')} tooltip={t('alerts.cooldownTooltip')} rules={[{ required: true }]}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
        )}
        <Form.Item name="channel_ids" label={t('alerts.notificationChannels')}>
          <Select mode="multiple" options={channels.map(c => ({ value: c.id, label: `${c.name} (${getChannelTypeLabels(t)[c.type]})` }))} />
        </Form.Item>
        <Form.Item name="is_active" label={t('users.active')} valuePropName="checked">
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  )
}
