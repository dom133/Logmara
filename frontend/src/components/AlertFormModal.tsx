import { Form, Input, InputNumber, Select, Switch, Space, Modal, Button } from 'antd'
import { Alert, AlertRequest, AlertRuleType, DeviceStats, Parser, ParsedField, NotificationChannel, resolveDeviceDisplayName } from '../services/api'
import { ruleTypeLabels, adminOnlyRuleTypes, operatorLabels, channelTypeLabels } from '../constants/alertConstants'

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
      title={editing ? 'Edit Alert Rule' : 'New Alert Rule'}
      open={open}
      onCancel={onCancel}
      onOk={onOk}
      width={{ sm: '90%', md: 640 }}
    >
      <Form form={form} layout="vertical">
        <Form.Item name="name" label="Name" rules={[{ required: true }]}>
          <Input />
        </Form.Item>
        <Form.Item name="description" label="Description">
          <Input.TextArea rows={2} />
        </Form.Item>
        <Form.Item name="rule_type" label="Rule Type" rules={[{ required: true }]}>
          <Select
            options={Object.entries(ruleTypeLabels)
              .filter(([key]) => isAdmin || !adminOnlyRuleTypes.includes(key))
              .map(([value, label]) => ({ value, label }))}
          />
        </Form.Item>

        {(ruleType === 'log_threshold' || ruleType === 'device_silence') && (
          <Form.Item name="device_ips" label="Devices" tooltip="Leave empty to watch all devices">
            <Select mode="multiple" allowClear placeholder="All devices" options={deviceOptions} />
          </Form.Item>
        )}

        {ruleType === 'log_threshold' && (
          <>
            <Form.Item name="severity" label="Minimum Severity" tooltip="Leave empty to match any severity">
              <Select allowClear options={['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug'].map(s => ({ value: s, label: s }))} />
            </Form.Item>
            <Form.Item name="parser_names" label="Parsers" tooltip="Which parser(s) must have matched the log entry; leave empty to match any parser (including unparsed logs). Narrowed to parsers actually seen on the selected device(s) once you pick one.">
              <Select mode="multiple" allowClear placeholder="Any parser" options={parserOptions} />
            </Form.Item>
            <Form.Item name="message_pattern" label="Message Pattern" tooltip="Substring or glob (*) match against the raw log message">
              <Input placeholder="failed login" />
            </Form.Item>

            {ruleType === 'log_threshold' && (
              <>
                <Form.Item name="field_conditions_logic" label="Combine conditions with" hidden={fieldConditions.length <= 1} style={{ maxWidth: 220, marginBottom: 8 }}>
                  <Select options={[
                    { value: 'and', label: 'AND (all must match)' },
                    { value: 'or', label: 'OR (any must match)' },
                  ]} />
                </Form.Item>
                <Form.List name="field_conditions">
                  {(fields, { add, remove }) => (
                    <>
                      {fields.map((field) => (
                        <Space key={field.key} align="baseline" style={{ display: 'flex', marginBottom: 8 }} wrap>
                          <Form.Item name={[field.name, 'field_name']} rules={[{ required: true, message: 'Field required' }]} noStyle>
                            <Select showSearch placeholder="Field" style={{ width: 200 }} options={fieldOptions} />
                          </Form.Item>
                          <Form.Item name={[field.name, 'operator']} initialValue="equals" noStyle>
                            <Select style={{ width: 130 }} options={Object.entries(operatorLabels).map(([value, label]) => ({ value, label }))} />
                          </Form.Item>
                          <Form.Item name={[field.name, 'value']} rules={[{ required: true, message: 'Value required' }]} noStyle>
                            <Input placeholder="Value" style={{ width: 180 }} />
                          </Form.Item>
                          <Button type="link" danger onClick={() => remove(field.name)}>Remove</Button>
                        </Space>
                      ))}
                      <Button type="dashed" onClick={() => add({ operator: 'equals' })} block>+ Add Field Condition</Button>
                    </>
                  )}
                </Form.List>
              </>
            )}

            <Form.Item name="fire_on_every_match" label="Fire on every match" valuePropName="checked" tooltip="Notify for every matching log entry as it arrives, instead of counting matches against a threshold. Ignores Threshold, Window, and Cooldown below.">
              <Switch />
            </Form.Item>

            {!fireOnEveryMatch && (
              <Space.Compact block>
                <Form.Item name="threshold" label="Threshold (matches)" style={{ flex: 1 }} rules={[{ required: true }]}>
                  <InputNumber min={1} style={{ width: '100%' }} />
                </Form.Item>
                <Form.Item name="window_minutes" label="Window (minutes)" style={{ flex: 1 }} rules={[{ required: true }]}>
                  <InputNumber min={1} style={{ width: '100%' }} />
                </Form.Item>
              </Space.Compact>
            )}
          </>
        )}

        {ruleType === 'device_silence' && (
          <Form.Item name="threshold" label="Silent After (minutes)" rules={[{ required: true }]}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
        )}

        {ruleType === 'audit_log' && (
          <Form.Item name="audit_action_filter" label="Action Filter" tooltip="Leave empty to match any audit action">
            <Select placeholder="Select an audit action (or leave empty for all)" allowClear>
              <Select.OptGroup label="Authentication">
                <Select.Option value="login_success">login_success</Select.Option>
                <Select.Option value="login_failed">login_failed</Select.Option>
                <Select.Option value="login_failed_lockout">login_failed_lockout</Select.Option>
                <Select.Option value="login_failed_inactive">login_failed_inactive</Select.Option>
                <Select.Option value="refresh_success">refresh_success</Select.Option>
                <Select.Option value="refresh_failed">refresh_failed</Select.Option>
                <Select.Option value="refresh_race_recovered">refresh_race_recovered</Select.Option>
                <Select.Option value="password_changed">password_changed</Select.Option>
              </Select.OptGroup>
              <Select.OptGroup label="User Management">
                <Select.Option value="user_created">user_created</Select.Option>
                <Select.Option value="user_updated">user_updated</Select.Option>
                <Select.Option value="user_deleted">user_deleted</Select.Option>
                <Select.Option value="password_reset_by_admin">password_reset_by_admin</Select.Option>
                <Select.Option value="user_unlocked">user_unlocked</Select.Option>
              </Select.OptGroup>
              <Select.OptGroup label="Settings">
                <Select.Option value="settings_updated">settings_updated</Select.Option>
                <Select.Option value="logs_purged">logs_purged</Select.Option>
              </Select.OptGroup>
              <Select.OptGroup label="SSL/TLS">
                <Select.Option value="ssl_cert_uploaded">ssl_cert_uploaded</Select.Option>
              </Select.OptGroup>
              <Select.OptGroup label="Relay">
                <Select.Option value="relay_certificate_issued">relay_certificate_issued</Select.Option>
                <Select.Option value="relay_certificate_revoked">relay_certificate_revoked</Select.Option>
                <Select.Option value="relay_whitelist_added">relay_whitelist_added</Select.Option>
                <Select.Option value="relay_whitelist_removed">relay_whitelist_removed</Select.Option>
              </Select.OptGroup>
              <Select.OptGroup label="System">
                <Select.Option value="slow_query">slow_query</Select.Option>
                <Select.Option value="bulk_operation">bulk_operation</Select.Option>
              </Select.OptGroup>
            </Select>
          </Form.Item>
        )}

        {ruleType === 'relay_cert_expiring' && (
          <Form.Item name="threshold" label="Warn Before Expiry (days)" tooltip="Fires once per relay certificate that's within this many days of its own expiry (checked hourly), subject to the cooldown below" rules={[{ required: true }]}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
        )}

        {ruleType === 'malformed_json' && (
          <Form.Item name="fire_on_every_match" label="Fire on every match" valuePropName="checked" tooltip="Notify for every malformed JSON line encountered during ingestion, instead of at most once per cooldown period below.">
            <Switch />
          </Form.Item>
        )}

        {!((ruleType === 'log_threshold' || ruleType === 'malformed_json') && fireOnEveryMatch) && (
          <Form.Item name="cooldown_minutes" label="Cooldown (minutes)" tooltip="Minimum time between repeat notifications for this rule" rules={[{ required: true }]}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
        )}
        <Form.Item name="channel_ids" label="Notification Channels">
          <Select mode="multiple" options={channels.map(c => ({ value: c.id, label: `${c.name} (${channelTypeLabels[c.type]})` }))} />
        </Form.Item>
        <Form.Item name="is_active" label="Active" valuePropName="checked">
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  )
}
