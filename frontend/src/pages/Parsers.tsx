import { useState } from 'react'
import { Card, Table, Button, Tag, Space, Modal, Form, Input, Select, Switch, message, Popconfirm, Tooltip, Typography, Divider, Descriptions, Row, Col, Statistic } from 'antd'
import { PlusOutlined, PlayCircleOutlined, ReloadOutlined, DeleteOutlined, EditOutlined, RestOutlined, CopyOutlined } from '@ant-design/icons'
import { getParsers, createParser, updateParser, deleteParser, cloneParser, testParser, reparseUnparsed, getParsedFields, Parser, ParsedField, ParserFieldDef } from '../services/api'
import { useColumnWidths } from '../hooks/useColumnWidths'
import { useCrud } from '../hooks/useCRUD'
import { useAuth } from '../services/auth'
import { getErrorMessage } from '../utils/error'

const { Title, Text } = Typography

const deviceTypes = ['all', 'mikrotik', 'ubiquiti', 'cisco', 'palo_alto', 'pfsense', 'linux', 'generic']
const matchTypes = ['hostname', 'app_name', 'message', 'all']

export default function ParsersPage() {
  const { user } = useAuth()
  const canEdit = user?.role === 'admin' || user?.role === 'editor'
  const [form] = Form.useForm()
  const [fields, setFields] = useState<ParsedField[]>([])
  const [testModalOpen, setTestModalOpen] = useState(false)
  const [testResult, setTestResult] = useState<{ matched: boolean; parser_name: string | null; fields: Record<string, string> | null; message: string } | null>(null)
  const [testLoading, setTestLoading] = useState(false)
  const [sampleLog, setSampleLog] = useState('')
  const [pattern, setPattern] = useState('')

  const { enhanceColumns, hasChanges, reset } = useColumnWidths(
    'col_widths_parsers',
    [
      { key: 'name', width: 180 },
      { key: 'device_type', width: 120 },
      { key: 'match', width: 180 },
      { key: 'regex', width: 250 },
      { key: 'fields', width: 180 },
      { key: 'enabled', width: 100 },
      { key: 'actions', width: 180 },
    ],
  )

  const {
    items: parsers,
    loading,
    modalOpen,
    editing,
    openCreate,
    openEdit: crudOpenEdit,
    closeModal,
    handleCreate,
    handleUpdate,
    handleDelete,
    refresh,
  } = useCrud<Parser, { name: string; description?: string; device_type: string; match_type: string; match_value?: string; regex: string; enabled: boolean; fields: ParserFieldDef[] }, Partial<{ name: string; description: string; device_type: string; match_type: string; match_value: string; regex: string; enabled: boolean; fields: ParserFieldDef[] }>>({
    loadData: async () => {
      const [p, f] = await Promise.all([getParsers(), getParsedFields()])
      setFields(f)
      return p
    },
    createItem: (data) => createParser(data as { name: string; description?: string; device_type: string; match_type: string; match_value?: string; regex: string; enabled: boolean; fields: ParserFieldDef[] }),
    updateItem: updateParser,
    deleteItem: deleteParser,
    entityName: 'Parser',
    form,
  })

  const parserFields = (pid: number) => fields.filter(f => f.parser_id === pid)

  const openEdit = (p: Parser) => {
    form.setFieldsValue({
      name: p.name,
      description: p.description ?? undefined,
      device_type: p.device_type,
      match_type: p.match_type,
      match_value: p.match_value ?? undefined,
      regex: p.regex,
      enabled: p.enabled,
      fields: parserFields(p.id).map(f => ({ name: f.field_name, label: f.field_label, type: f.field_type })),
    })
    crudOpenEdit(p)
  }

  const handleClone = async (id: number) => {
    try {
      await cloneParser(id)
      message.success('Parser cloned')
      refresh()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to clone parser'))
    }
  }

  const handleTest = async () => {
    if (!pattern.trim() || !sampleLog.trim()) {
      message.warning('Enter both pattern and sample log')
      return
    }
    setTestLoading(true)
    try {
      const res = await testParser(pattern, sampleLog)
      setTestResult(res)
    } catch (e: unknown) {
      const msg = getErrorMessage(e, 'Test failed')
      message.error(msg)
      setTestResult({ matched: false, parser_name: null, fields: null, message: msg })
    } finally {
      setTestLoading(false)
    }
  }

  const handleReparse = async () => {
    try {
      await reparseUnparsed()
      message.success('Reparse started in background')
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to start reparse'))
    }
  }

  const columns = [
    {
      title: 'Name',
      dataIndex: 'name',
      key: 'name',
      render: (v: string, r: Parser) => (
        <Space>
          <Text strong>{v}</Text>
          {r.is_builtin && <Tag color="purple">Built-in</Tag>}
        </Space>
      ),
    },
    {
      title: 'Device Type',
      dataIndex: 'device_type',
      key: 'device_type',
      render: (v: string) => <Tag color="blue">{v}</Tag>,
    },
    {
      title: 'Match',
      key: 'match',
      width: 180,
      ellipsis: true,
      render: (_v: unknown, r: Parser) => `${r.match_type}: ${r.match_value || '-'}`,
    },
    {
      title: 'Regex',
      dataIndex: 'regex',
      key: 'regex',
      width: 250,
      ellipsis: true,
      render: (v: string) => <Text code style={{ fontSize: 11 }}>{v}</Text>,
    },
    {
      title: 'Fields',
      key: 'fields',
      render: (_v: unknown, r: Parser) => {
        const fs = parserFields(r.id)
        return fs.length ? fs.map(f => <Tag key={f.field_name}>{f.field_label}</Tag>) : <Text type="secondary">-</Text>
      },
    },
    {
      title: 'Status',
      dataIndex: 'enabled',
      key: 'enabled',
      render: (v: boolean) => <Tag color={v ? 'green' : 'red'}>{v ? 'Enabled' : 'Disabled'}</Tag>,
    },
    {
      title: 'Actions',
      key: 'actions',
      render: (_v: unknown, r: Parser) => (
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {canEdit && <Tooltip title="Edit">
            <Button size="small" icon={<EditOutlined />} disabled={r.is_builtin} onClick={() => openEdit(r)} />
          </Tooltip>}
          {canEdit && <Tooltip title="Clone">
            <Button size="small" icon={<CopyOutlined />} onClick={() => handleClone(r.id)} />
          </Tooltip>}
          {canEdit && <Popconfirm title="Delete parser?" onConfirm={() => handleDelete(r.id)} disabled={r.is_builtin}>
            <Button size="small" danger icon={<DeleteOutlined />} disabled={r.is_builtin} />
          </Popconfirm>}
        </div>
      ),
    },
  ]

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 8, marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0, whiteSpace: 'nowrap' }}>Parser Engine</Title>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>Reset Columns</Button>}
          {canEdit && <Button icon={<ReloadOutlined />} onClick={handleReparse}>Reparse Unparsed</Button>}
          {canEdit && <Button icon={<PlayCircleOutlined />} onClick={() => setTestModalOpen(true)}>Test Regex</Button>}
          {canEdit && <Button type="primary" icon={<PlusOutlined />} onClick={() => {
            form.setFieldsValue({ enabled: true, match_type: 'hostname', device_type: 'all', fields: [] })
            openCreate()
          }}>New Parser</Button>}
        </div>
      </div>

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} sm={12} md={6}>
          <Card size="small"><Statistic title="Total Parsers" value={parsers.length} /></Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card size="small"><Statistic title="Built-in" value={parsers.filter(p => p.is_builtin).length} /></Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card size="small"><Statistic title="Custom" value={parsers.filter(p => !p.is_builtin).length} /></Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card size="small"><Statistic title="Field Definitions" value={fields.length} /></Card>
        </Col>
      </Row>

      <Table
        dataSource={parsers}
        columns={enhanceColumns(columns)}
        rowKey="id"
        loading={loading}
        size="small"
        tableLayout="fixed"
        pagination={{ pageSize: 20 }}
        scroll={{ x: 'max-content' }}
      />

      <Modal
        title={editing ? 'Edit Parser' : 'New Parser'}
        open={modalOpen}
        onCancel={closeModal}
        onOk={() => { form.validateFields().then(values => editing ? handleUpdate(values) : handleCreate(values)) }}
        width={{ sm: '90%', md: 700 }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="description" label="Description">
            <Input.TextArea rows={2} />
          </Form.Item>
          <Form.Item name="device_type" label="Device Type" rules={[{ required: true }]}>
            <Select options={deviceTypes.map(d => ({ label: d, value: d }))} />
          </Form.Item>
          <Form.Item name="match_type" label="Match Type" rules={[{ required: true }]}>
            <Select options={matchTypes.map(m => ({ label: m, value: m }))} />
          </Form.Item>
          <Form.Item name="match_value" label="Match Value (glob patterns supported)">
            <Input placeholder="e.g. router*, *.lan" />
          </Form.Item>
          <Form.Item name="regex" label="Regex (with named groups)" rules={[{ required: true }]}>
            <Input.TextArea rows={3} placeholder='(?P<action>\w+) (?P<ip>\d+\.\d+\.\d+\.\d+)' />
          </Form.Item>
          <Form.Item name="enabled" label="Enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Divider />
          <Text>Field definitions (named groups in regex will auto-extract)</Text>
          <Form.List name="fields">
            {(fieldsList, { add, remove }) => (
              <>
                {fieldsList.map((field, index) => (
                  <div key={field.key} style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'baseline', marginBottom: 8 }}>
                    <Form.Item name={[index, 'name']} rules={[{ required: true }]}>
                      <Input placeholder="Field name" style={{ width: 150 }} />
                    </Form.Item>
                    <Form.Item name={[index, 'label']}>
                      <Input placeholder="Label" style={{ width: 150 }} />
                    </Form.Item>
                    <Form.Item name={[index, 'type']}>
                      <Select options={['string', 'number', 'ip', 'mac', 'duration'].map(t => ({ label: t, value: t }))} style={{ width: 120 }} />
                    </Form.Item>
                    <Button type="link" danger onClick={() => remove(index)}>Remove</Button>
                  </div>
                ))}
                <Button type="dashed" onClick={() => add({ name: '', label: '', type: 'string' })} block>+ Add Field</Button>
              </>
            )}
          </Form.List>
        </Form>
      </Modal>

      <Modal
        title="Test Regex Pattern"
        open={testModalOpen}
        onCancel={() => { setTestModalOpen(false); setTestResult(null) }}
        footer={null}
        width={{ sm: '95%', md: 800 }}
      >
        <Form layout="vertical">
          <Form.Item label="Regex Pattern">
            <Input.TextArea rows={2} value={pattern} onChange={e => setPattern(e.target.value)} placeholder='(?P<action>\w+) (?P<ip>\d+\.\d+\.\d+\.\d+)' />
          </Form.Item>
          <Form.Item label="Sample Log Message">
            <Input.TextArea rows={4} value={sampleLog} onChange={e => setSampleLog(e.target.value)} placeholder="Paste a log line here..." />
          </Form.Item>
          <Button type="primary" icon={<PlayCircleOutlined />} onClick={handleTest} loading={testLoading} block>Test</Button>
        </Form>
        {testResult && (
          <Card
            style={{ marginTop: 16 }}
            type="inner"
            title={testResult.matched ? '✅ Matched' : '❌ No Match'}
          >
            {testResult.parser_name && <p><Text strong>Parser:</Text> {testResult.parser_name}</p>}
            {testResult.fields && Object.keys(testResult.fields).length > 0 && (
              <Descriptions column={2} size="small">
                {Object.entries(testResult.fields).map(([k, v]) => (
                  <Descriptions.Item key={k} label={k}>
                    <span style={{ wordBreak: 'break-all' }}>{v}</span>
                  </Descriptions.Item>
                ))}
              </Descriptions>
            )}
            {!testResult.matched && <Text type="secondary">{testResult.message}</Text>}
          </Card>
        )}
      </Modal>
    </>
  )
}
