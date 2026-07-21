import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Card, Table, Button, Tag, Space, Modal, Form, Input, InputNumber, Select, Switch, message, Popconfirm, Tabs, Typography, Descriptions, List, Empty } from 'antd'
import { PlusOutlined, DeleteOutlined, EditOutlined, ExperimentOutlined, EyeOutlined, CheckCircleOutlined } from '@ant-design/icons'
import {
  getAlerts, createAlert, updateAlert, deleteAlert, Alert, AlertRequest, AlertRuleType, FieldConditionOperator,
  getNotificationChannels, createNotificationChannel, updateNotificationChannel, deleteNotificationChannel, testNotificationChannel,
  NotificationChannel, NotificationChannelRequest, NotificationChannelType,
  getNotificationHistory, clearNotificationHistory, NotificationLogEntry,
  getDevices, DeviceStats, resolveDeviceDisplayName, getParsers, Parser, getParsedFields, ParsedField,
} from '../services/api'
import { useAuth } from '../services/auth'
import { onLiveNotification } from '../services/notificationEvents'
import { getErrorMessage } from '../utils/error'
import SeverityTag from '../components/SeverityTag'

const { Title, Text } = Typography

const ruleTypeLabels: Record<AlertRuleType, string> = {
  log_threshold: 'Log threshold',
  device_silence: 'Device silence',
  config_change: 'Config change',
}

const channelTypeLabels: Record<NotificationChannelType, string> = {
  email: 'Email',
  webhook: 'Webhook',
  slack: 'Slack',
  teams: 'Microsoft Teams',
  in_app: 'In-app',
  push: 'Push (browser)',
}

const operatorLabels: Record<FieldConditionOperator, string> = {
  equals: 'Equals',
  contains: 'Contains',
  not_equals: 'Not equals',
  regex: 'Regex',
}

function RulesTab({ canEdit, active }: { canEdit: boolean; active: boolean }) {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [channels, setChannels] = useState<NotificationChannel[]>([])
  const [devices, setDevices] = useState<DeviceStats[]>([])
  const [parsers, setParsers] = useState<Parser[]>([])
  const [parsedFields, setParsedFields] = useState<ParsedField[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Alert | null>(null)
  const [form] = Form.useForm()
  const ruleType = Form.useWatch('rule_type', form)
  const selectedParsers: string[] = Form.useWatch('parser_names', form) || []
  const selectedDevices: string[] = Form.useWatch('device_ips', form) || []
  const selectedDevicesKey = selectedDevices.join(',')

  const loadData = async () => {
    setLoading(true)
    try {
      const [a, c, d, p] = await Promise.all([getAlerts(), getNotificationChannels(), getDevices(), getParsers()])
      setAlerts(a)
      setChannels(c)
      setDevices(d)
      setParsers(p)
    } catch {
      message.error('Failed to load alerts')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadData() }, [])

  // A firing alert updates this rule's last_fired_at - refresh the list
  // live while this tab is the one actually showing, rather than making the
  // user switch away and back to see it.
  useEffect(() => {
    if (!active) return
    return onLiveNotification(() => { loadData() })
  }, [active])

  // Refetch the field registry scoped to the selected device(s) whenever
  // that selection changes, so "Field Conditions" only offers fields that
  // parsers have actually extracted from those devices' logs (falls back to
  // every known field when no device is selected).
  useEffect(() => {
    getParsedFields(selectedDevices.length > 0 ? selectedDevices : undefined)
      .then(setParsedFields)
      .catch(() => { /* keep the previous list rather than blanking the form */ })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedDevicesKey])

  const deviceOptions = devices.map(d => ({ label: resolveDeviceDisplayName(d), value: d.fromhost_ip }))
  // Only parsers that have actually matched a log from at least one of the
  // selected device(s) - an empty selection means "all devices", so show
  // every parser, same as before this was device-scoped.
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

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({
      rule_type: 'log_threshold', is_active: true, window_minutes: 5, cooldown_minutes: 15, threshold: 5,
      channel_ids: [], device_ips: [], parser_names: [], field_conditions: [],
    })
    setModalOpen(true)
  }

  const openEdit = (a: Alert) => {
    setEditing(a)
    form.setFieldsValue(a)
    setModalOpen(true)
  }

  const handleSubmit = async () => {
    const values = await form.validateFields() as AlertRequest
    try {
      if (editing) {
        await updateAlert(editing.id, values)
        message.success('Alert rule updated')
      } else {
        await createAlert(values)
        message.success('Alert rule created')
      }
      setModalOpen(false)
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to save alert rule'))
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteAlert(id)
      message.success('Alert rule deleted')
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to delete alert rule'))
    }
  }

  const channelName = (id: number) => channels.find(c => c.id === id)?.name || `#${id}`

  const columns = [
    { title: 'Name', dataIndex: 'name', key: 'name', render: (v: string, r: Alert) => <Space>{v}{!r.is_active && <Tag>Disabled</Tag>}</Space> },
    { title: 'Type', dataIndex: 'rule_type', key: 'rule_type', render: (v: AlertRuleType) => <Tag color="blue">{ruleTypeLabels[v]}</Tag> },
    {
      title: 'Condition', key: 'condition', ellipsis: true,
      render: (_v: unknown, r: Alert) => {
        const scope = [
          (r.device_ips || []).length > 0 ? `${r.device_ips.length} device(s)` : 'all devices',
          (r.parser_names || []).length > 0 ? `${r.parser_names.length} parser(s)` : null,
          (r.field_conditions || []).length > 0 ? `${r.field_conditions.length} field condition(s)` : null,
        ].filter(Boolean).join(', ')
        if (r.rule_type === 'log_threshold') {
          return `${r.threshold}+ matches / ${r.window_minutes}m on ${scope}${r.severity ? `, severity >= ${r.severity}` : ''}`
        }
        if (r.rule_type === 'device_silence') {
          return `silent for ${r.threshold}m on ${scope}`
        }
        return r.audit_action_filter ? `action = ${r.audit_action_filter}` : 'any config change'
      },
    },
    {
      title: 'Channels', key: 'channels',
      render: (_v: unknown, r: Alert) => (r.channel_ids || []).map(id => <Tag key={id}>{channelName(id)}</Tag>),
    },
    { title: 'Last Fired', dataIndex: 'last_fired_at', key: 'last_fired_at', render: (v?: string) => v ? new Date(v).toLocaleString() : '-' },
    {
      title: 'Actions', key: 'actions',
      render: (_v: unknown, r: Alert) => (
        <Space>
          {canEdit && <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />}
          {canEdit && <Popconfirm title="Delete alert rule?" onConfirm={() => handleDelete(r.id)}>
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>}
        </Space>
      ),
    },
  ]

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        {canEdit && <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>New Alert Rule</Button>}
      </div>
      <Table dataSource={alerts} columns={columns} rowKey="id" loading={loading} size="small" scroll={{ x: 'max-content' }} />

      <Modal
        title={editing ? 'Edit Alert Rule' : 'New Alert Rule'}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={handleSubmit}
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
            <Select options={Object.entries(ruleTypeLabels).map(([value, label]) => ({ value, label }))} />
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

              <Form.Item label="Field Conditions" tooltip="All conditions must match (AND). Fields come from the parsers selected above, or all fields seen on the selected device(s) if no parser is selected, or every known field if neither is selected.">
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
              </Form.Item>

              <Space.Compact block>
                <Form.Item name="threshold" label="Threshold (matches)" style={{ flex: 1 }} rules={[{ required: true }]}>
                  <InputNumber min={1} style={{ width: '100%' }} />
                </Form.Item>
                <Form.Item name="window_minutes" label="Window (minutes)" style={{ flex: 1 }} rules={[{ required: true }]}>
                  <InputNumber min={1} style={{ width: '100%' }} />
                </Form.Item>
              </Space.Compact>
            </>
          )}

          {ruleType === 'device_silence' && (
            <Form.Item name="threshold" label="Silent After (minutes)" rules={[{ required: true }]}>
              <InputNumber min={1} style={{ width: '100%' }} />
            </Form.Item>
          )}

          {ruleType === 'config_change' && (
            <Form.Item name="audit_action_filter" label="Action Filter" tooltip="e.g. settings_updated, user_created - leave empty to match any admin action">
              <Input placeholder="settings_updated" />
            </Form.Item>
          )}

          <Form.Item name="cooldown_minutes" label="Cooldown (minutes)" tooltip="Minimum time between repeat notifications for this rule" rules={[{ required: true }]}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="channel_ids" label="Notification Channels">
            <Select mode="multiple" options={channels.map(c => ({ value: c.id, label: `${c.name} (${channelTypeLabels[c.type]})` }))} />
          </Form.Item>
          <Form.Item name="is_active" label="Active" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}

function ChannelsTab({ canEdit }: { canEdit: boolean }) {
  const [channels, setChannels] = useState<NotificationChannel[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<NotificationChannel | null>(null)
  const [testingId, setTestingId] = useState<number | null>(null)
  const [form] = Form.useForm()
  const channelType = Form.useWatch('type', form)

  const loadData = async () => {
    setLoading(true)
    try {
      setChannels(await getNotificationChannels())
    } catch {
      message.error('Failed to load notification channels')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadData() }, [])

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ type: 'email', enabled: true })
    setModalOpen(true)
  }

  const openEdit = (c: NotificationChannel) => {
    setEditing(c)
    form.resetFields()
    form.setFieldsValue({
      name: c.name,
      type: c.type,
      enabled: c.enabled,
      to: (c.config?.to as string[] | undefined) || [],
      url: c.config?.url as string | undefined,
      webhook_url: c.config?.webhook_url as string | undefined,
    })
    setModalOpen(true)
  }

  const handleSubmit = async () => {
    const values = await form.validateFields()
    const config: Record<string, unknown> = {}
    if (values.type === 'email') config.to = values.to || []
    if (values.type === 'webhook') config.url = values.url
    if (values.type === 'slack' || values.type === 'teams') config.webhook_url = values.webhook_url

    const req: NotificationChannelRequest = {
      name: values.name,
      type: values.type,
      enabled: values.enabled,
      config,
      secret: values.secret || undefined,
    }

    try {
      if (editing) {
        await updateNotificationChannel(editing.id, req)
        message.success('Channel updated')
      } else {
        await createNotificationChannel(req)
        message.success('Channel created')
      }
      setModalOpen(false)
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to save channel'))
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteNotificationChannel(id)
      message.success('Channel deleted')
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to delete channel'))
    }
  }

  const handleTest = async (id: number) => {
    setTestingId(id)
    try {
      await testNotificationChannel(id)
      message.success('Test notification sent')
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Test notification failed'))
    } finally {
      setTestingId(null)
    }
  }

  const columns = [
    { title: 'Name', dataIndex: 'name', key: 'name' },
    { title: 'Type', dataIndex: 'type', key: 'type', render: (v: NotificationChannelType) => <Tag color="blue">{channelTypeLabels[v]}</Tag> },
    { title: 'Status', dataIndex: 'enabled', key: 'enabled', render: (v: boolean) => <Tag color={v ? 'green' : 'red'}>{v ? 'Enabled' : 'Disabled'}</Tag> },
    {
      title: 'Actions', key: 'actions',
      render: (_v: unknown, r: NotificationChannel) => (
        <Space>
          <Button size="small" icon={<ExperimentOutlined />} loading={testingId === r.id} onClick={() => handleTest(r.id)}>Test</Button>
          {canEdit && <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />}
          {canEdit && <Popconfirm title="Delete channel?" onConfirm={() => handleDelete(r.id)}>
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>}
        </Space>
      ),
    },
  ]

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        {canEdit && <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>New Channel</Button>}
      </div>
      <Table dataSource={channels} columns={columns} rowKey="id" loading={loading} size="small" scroll={{ x: 'max-content' }} />

      <Modal
        title={editing ? 'Edit Notification Channel' : 'New Notification Channel'}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={handleSubmit}
        width={{ sm: '90%', md: 560 }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="type" label="Type" rules={[{ required: true }]}>
            <Select options={Object.entries(channelTypeLabels).map(([value, label]) => ({ value, label }))} disabled={!!editing} />
          </Form.Item>

          {channelType === 'email' && (
            <Form.Item name="to" label="Recipients" rules={[{ required: true }]} tooltip="Uses the SMTP relay configured under Admin > Settings">
              <Select mode="tags" open={false} placeholder="you@example.com" tokenSeparators={[',', ' ']} />
            </Form.Item>
          )}
          {channelType === 'webhook' && (
            <>
              <Form.Item name="url" label="Webhook URL" rules={[{ required: true }]}>
                <Input placeholder="https://example.com/hook" />
              </Form.Item>
              <Form.Item name="secret" label="Bearer Token" tooltip="Optional - sent as an Authorization: Bearer header. Leave empty to keep the current one.">
                <Input.Password />
              </Form.Item>
            </>
          )}
          {(channelType === 'slack' || channelType === 'teams') && (
            <Form.Item name="webhook_url" label="Incoming Webhook URL" rules={[{ required: true }]}>
              <Input placeholder="https://hooks.slack.com/services/..." />
            </Form.Item>
          )}
          {channelType === 'in_app' && (
            <Text type="secondary">Delivers to the notification bell for every signed-in user - no further configuration needed.</Text>
          )}
          {channelType === 'push' && (
            <Text type="secondary">Delivers a browser push notification to every device subscribed via the bell menu's "Enable push notifications" toggle - no further configuration needed.</Text>
          )}

          <Form.Item name="enabled" label="Enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}

const historyStatusColor: Record<string, string> = {
  sent: 'green',
  failed: 'red',
  no_channel: 'orange',
}

function HistoryTab({ isAdmin, active, focusInAppId, onFocusConsumed }: { isAdmin: boolean; active: boolean; focusInAppId?: number; onFocusConsumed: () => void }) {
  const [entries, setEntries] = useState<NotificationLogEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [clearing, setClearing] = useState(false)
  const [viewing, setViewing] = useState<NotificationLogEntry | null>(null)

  const loadData = () => {
    setLoading(true)
    getNotificationHistory().then(setEntries).catch(() => message.error('Failed to load notification history')).finally(() => setLoading(false))
  }

  useEffect(() => { loadData() }, [])

  // A new notification means a new row in notification_log - refresh the
  // list live while this tab is the one actually showing.
  useEffect(() => {
    if (!active) return
    return onLiveNotification(() => { loadData() })
  }, [active])

  // Coming from a bell notification click (see NotificationBell's
  // goToHistoryDetail): open the same entry's Details view automatically,
  // as if the user had clicked "Details" on it themselves. Only the most
  // recent 100 history rows are fetched, and test notifications are never
  // recorded in history at all - either way, tell the user instead of
  // silently leaving the tab with nothing selected.
  useEffect(() => {
    if (!focusInAppId || loading) return
    const match = entries.find((e) => e.in_app_notification_id === focusInAppId)
    if (match) {
      setViewing(match)
    } else {
      message.info('No matching history entry was found for that notification (it may be too old, or a test notification, which is never recorded in history).')
    }
    onFocusConsumed()
  }, [focusInAppId, loading, entries, onFocusConsumed])

  const handleClear = async () => {
    setClearing(true)
    try {
      await clearNotificationHistory()
      message.success('Notification history cleared')
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to clear notification history'))
    } finally {
      setClearing(false)
    }
  }

  const columns = [
    { title: 'Time', dataIndex: 'created_at', key: 'created_at', render: (v: string) => new Date(v).toLocaleString() },
    { title: 'Alert', dataIndex: 'alert_name', key: 'alert_name' },
    { title: 'Channel', dataIndex: 'channel_name', key: 'channel_name', render: (v: string, r: NotificationLogEntry) => `${v} (${r.channel_type})` },
    { title: 'Status', dataIndex: 'status', key: 'status', render: (v: string) => <Tag color={historyStatusColor[v] || 'default'}>{v}</Tag> },
    { title: 'Detail', dataIndex: 'detail', key: 'detail', ellipsis: true },
    {
      title: 'Actions', key: 'actions',
      render: (_v: unknown, r: NotificationLogEntry) => (
        <Button size="small" icon={<EyeOutlined />} onClick={() => setViewing(r)}>Details</Button>
      ),
    },
  ]

  return (
    <>
      {isAdmin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
          <Popconfirm title="Clear all notification history?" onConfirm={handleClear}>
            <Button danger loading={clearing} disabled={entries.length === 0}>Clear History</Button>
          </Popconfirm>
        </div>
      )}
      <Table
        dataSource={entries}
        columns={columns}
        rowKey="id"
        loading={loading}
        size="small"
        scroll={{ x: 'max-content' }}
        onRow={(r) => ({ onClick: () => setViewing(r), style: { cursor: 'pointer' } })}
      />

      <Modal
        title="Notification Detail"
        open={!!viewing}
        onCancel={() => setViewing(null)}
        footer={<Button onClick={() => setViewing(null)}>Close</Button>}
        width={{ sm: '90%', md: 640 }}
      >
        {viewing && (
          <Space direction="vertical" size="middle" style={{ width: '100%' }}>
            <Descriptions column={1} bordered size="small">
              <Descriptions.Item label="Time">{new Date(viewing.created_at).toLocaleString()}</Descriptions.Item>
              <Descriptions.Item label="Alert">{viewing.alert_name || '—'}</Descriptions.Item>
              <Descriptions.Item label="Channel">{viewing.channel_name} ({viewing.channel_type})</Descriptions.Item>
              <Descriptions.Item label="Status">
                <Tag color={historyStatusColor[viewing.status] || 'default'}>{viewing.status}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="Detail">
                <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', marginBottom: 0 }} copyable={!!viewing.detail}>
                  {viewing.detail || '—'}
                </Typography.Paragraph>
              </Descriptions.Item>
            </Descriptions>

            <div>
              <Text strong>Triggering log</Text>
              <div style={{ marginTop: 8 }}>
                {viewing.trigger_log ? (
                  <Descriptions column={1} bordered size="small">
                    <Descriptions.Item label="Time">{new Date(viewing.trigger_log.timestamp).toLocaleString()}</Descriptions.Item>
                    <Descriptions.Item label="Severity"><SeverityTag severity={viewing.trigger_log.severity} /></Descriptions.Item>
                    <Descriptions.Item label="Host">{viewing.trigger_log.hostname} ({viewing.trigger_log.fromhost_ip})</Descriptions.Item>
                    {viewing.trigger_log.app_name && (
                      <Descriptions.Item label="App">{viewing.trigger_log.app_name}</Descriptions.Item>
                    )}
                    <Descriptions.Item label="Message">
                      <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', marginBottom: 0 }} copyable>
                        {viewing.trigger_log.message}
                      </Typography.Paragraph>
                    </Descriptions.Item>
                  </Descriptions>
                ) : (
                  <Empty description="No log entry associated with this notification (e.g. a config-change alert)" image={Empty.PRESENTED_IMAGE_SIMPLE} />
                )}
              </div>
            </div>

            <div>
              <Text strong>Conditions met</Text>
              <div style={{ marginTop: 8 }}>
                {viewing.matched_conditions && viewing.matched_conditions.length > 0 ? (
                  <List
                    size="small"
                    bordered
                    dataSource={viewing.matched_conditions}
                    renderItem={(item) => (
                      <List.Item>
                        <Space align="start">
                          <CheckCircleOutlined style={{ color: '#52c41a', marginTop: 4 }} />
                          <span>{item}</span>
                        </Space>
                      </List.Item>
                    )}
                  />
                ) : (
                  <Empty description="No condition breakdown recorded for this notification" image={Empty.PRESENTED_IMAGE_SIMPLE} />
                )}
              </div>
            </div>
          </Space>
        )}
      </Modal>
    </>
  )
}

export default function AlertsPage() {
  const { user } = useAuth()
  const canEdit = user?.role === 'admin' || user?.role === 'editor'
  const isAdmin = user?.role === 'admin'

  // Deep-linked from a notification bell click (see NotificationBell's
  // goToHistoryDetail): ?tab=history&notification=<in_app_notification_id>
  // opens straight to History with that entry's Details already showing.
  const [searchParams] = useSearchParams()
  const initialTab = searchParams.get('tab') === 'history' ? 'history' : 'rules'
  const initialFocusID = Number(searchParams.get('notification')) || undefined
  const [activeTab, setActiveTab] = useState(initialTab)
  const [focusInAppId, setFocusInAppId] = useState<number | undefined>(initialFocusID)

  const items = [
    { key: 'rules', label: 'Alert Rules', children: <RulesTab canEdit={canEdit} active={activeTab === 'rules'} /> },
    { key: 'channels', label: 'Notification Channels', children: <ChannelsTab canEdit={isAdmin} /> },
    {
      key: 'history', label: 'History',
      children: (
        <HistoryTab
          isAdmin={isAdmin}
          active={activeTab === 'history'}
          focusInAppId={focusInAppId}
          onFocusConsumed={() => setFocusInAppId(undefined)}
        />
      ),
    },
  ]

  return (
    <>
      <Title level={3} style={{ marginTop: 0 }}>Alerts &amp; Notifications</Title>
      <Card>
        <Tabs items={items} activeKey={activeTab} onChange={setActiveTab} />
      </Card>
    </>
  )
}
