import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Card, Table, Button, Tag, Space, Modal, Form, Input, Select, Switch, message, Popconfirm, Tooltip, Typography, Divider, Descriptions, Row, Col, Statistic } from 'antd'
import { PlusOutlined, PlayCircleOutlined, ReloadOutlined, DeleteOutlined, EditOutlined, RestOutlined, CopyOutlined } from '@ant-design/icons'
import { getParsers, createParser, updateParser, deleteParser, cloneParser, testParser, reparseUnparsed, getParsedFields, Parser, ParsedField, ParserFieldDef } from '../services/api'
import { useColumnWidths } from '../hooks/useColumnWidths'
import { useCrud } from '../hooks/useCRUD'
import { useAuth } from '../services/auth'
import { getErrorMessage } from '../utils/error'
import { tokens } from '../theme/tokens'

const { Title, Text } = Typography

const deviceTypes = ['all', 'mikrotik', 'ubiquiti', 'cisco', 'palo_alto', 'pfsense', 'linux', 'windows', 'generic']
const matchTypes = ['hostname', 'app_name', 'message', 'all']

export default function ParsersPage() {
  const { t } = useTranslation()
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
      message.success(t('parsers.cloned'))
      refresh()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('parsers.cloneFailed')))
    }
  }

  const handleTest = async () => {
    if (!pattern.trim() || !sampleLog.trim()) {
      message.warning(t('parsers.enterPatternAndSample'))
      return
    }
    setTestLoading(true)
    try {
      const res = await testParser(pattern, sampleLog)
      setTestResult(res)
    } catch (e: unknown) {
      const msg = getErrorMessage(e, t('parsers.testFailed'))
      message.error(msg)
      setTestResult({ matched: false, parser_name: null, fields: null, message: msg })
    } finally {
      setTestLoading(false)
    }
  }

  const handleReparse = async () => {
    try {
      await reparseUnparsed()
      message.success(t('parsers.reparseStarted'))
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('parsers.reparseFailed')))
    }
  }

  const columns = [
    {
      title: t('common.name'),
      dataIndex: 'name',
      key: 'name',
      render: (v: string, r: Parser) => (
        <Space>
          <Text strong>{v}</Text>
          {r.is_builtin && <Tag color="purple">{t('parsers.builtIn')}</Tag>}
        </Space>
      ),
    },
    {
      title: t('parsers.deviceType'),
      dataIndex: 'device_type',
      key: 'device_type',
      render: (v: string) => <Tag color="blue">{v}</Tag>,
    },
    {
      title: t('parsers.match'),
      key: 'match',
      width: 180,
      ellipsis: true,
      render: (_v: unknown, r: Parser) => `${r.match_type}: ${r.match_value || '-'}`,
    },
    {
      title: t('parsers.regex'),
      dataIndex: 'regex',
      key: 'regex',
      width: 250,
      ellipsis: true,
      render: (v: string) => <Text code style={{ fontSize: 11 }}>{v}</Text>,
    },
    {
      title: t('common.fields'),
      key: 'fields',
      render: (_v: unknown, r: Parser) => {
        const fs = parserFields(r.id)
        return fs.length ? fs.map(f => <Tag key={f.field_name}>{f.field_label}</Tag>) : <Text type="secondary">-</Text>
      },
    },
    {
      title: t('common.status'),
      dataIndex: 'enabled',
      key: 'enabled',
      render: (v: boolean) => <Tag color={v ? 'green' : 'red'}>{v ? t('common.enabled') : t('common.disabled')}</Tag>,
    },
    {
      title: t('common.actions'),
      key: 'actions',
      render: (_v: unknown, r: Parser) => (
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {canEdit && <Tooltip title={t('common.edit')}>
            <Button size="small" icon={<EditOutlined />} disabled={r.is_builtin} onClick={() => openEdit(r)} />
          </Tooltip>}
          {canEdit && <Tooltip title={t('parsers.clone')}>
            <Button size="small" icon={<CopyOutlined />} onClick={() => handleClone(r.id)} />
          </Tooltip>}
          {canEdit && <Popconfirm title={t('parsers.deleteConfirm')} onConfirm={() => handleDelete(r.id)} disabled={r.is_builtin}>
            <Button size="small" danger icon={<DeleteOutlined />} disabled={r.is_builtin} />
          </Popconfirm>}
        </div>
      ),
    },
  ]

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 8, marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0, whiteSpace: 'nowrap' }}>{t('parsers.title')}</Title>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>{t('parsers.resetColumns')}</Button>}
          {canEdit && <Button icon={<ReloadOutlined />} onClick={handleReparse}>{t('parsers.reparseUnparsed')}</Button>}
          {canEdit && <Button icon={<PlayCircleOutlined />} onClick={() => setTestModalOpen(true)}>{t('parsers.testRegex')}</Button>}
          {canEdit && <Button type="primary" icon={<PlusOutlined />} onClick={() => {
            form.setFieldsValue({ enabled: true, match_type: 'hostname', device_type: 'all', fields: [] })
            openCreate()
          }}>{t('parsers.new')}</Button>}
        </div>
      </div>

      <Row gutter={tokens.spacing.md} style={{ marginBottom: tokens.spacing.md }}>
        <Col xs={24} sm={12} md={6}>
          <Card size="small"><Statistic title={t('parsers.totalParsers')} value={parsers.length} /></Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card size="small"><Statistic title={t('parsers.builtInCount')} value={parsers.filter(p => p.is_builtin).length} /></Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card size="small"><Statistic title={t('parsers.customCount')} value={parsers.filter(p => !p.is_builtin).length} /></Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card size="small"><Statistic title={t('parsers.fieldDefinitions')} value={fields.length} /></Card>
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
        title={editing ? t('parsers.editTitle') : t('parsers.newTitle')}
        open={modalOpen}
        onCancel={closeModal}
        onOk={() => { form.validateFields().then(values => editing ? handleUpdate(values) : handleCreate(values)) }}
        width={{ sm: '90%', md: 700 }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label={t('common.name')} rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="description" label={t('common.description')}>
            <Input.TextArea rows={2} />
          </Form.Item>
          <Form.Item name="device_type" label={t('parsers.deviceType')} rules={[{ required: true }]}>
            <Select options={deviceTypes.map(d => ({ label: d, value: d }))} />
          </Form.Item>
          <Form.Item name="match_type" label={t('parsers.matchType')} rules={[{ required: true }]}>
            <Select options={matchTypes.map(m => ({ label: m, value: m }))} />
          </Form.Item>
          <Form.Item name="match_value" label={t('parsers.matchValue')}>
            <Input placeholder={t('parsers.matchValuePlaceholder')} />
          </Form.Item>
          <Form.Item name="regex" label={t('parsers.regexLabel')} rules={[{ required: true }]}>
            <Input.TextArea rows={3} placeholder='(?P<action>\w+) (?P<ip>\d+\.\d+\.\d+\.\d+)' />
          </Form.Item>
          <Form.Item name="enabled" label={t('common.enabled')} valuePropName="checked">
            <Switch />
          </Form.Item>
          <Divider />
          <Text>{t('parsers.fieldDefsNote')}</Text>
          <Form.List name="fields">
            {(fieldsList, { add, remove }) => (
              <>
                {fieldsList.map((field, index) => (
                  <div key={field.key} style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'baseline', marginBottom: 8 }}>
                    <Form.Item name={[index, 'name']} rules={[{ required: true }]}>
                      <Input placeholder={t('parsers.fieldNamePlaceholder')} style={{ width: 150 }} />
                    </Form.Item>
                    <Form.Item name={[index, 'label']}>
                      <Input placeholder={t('common.label')} style={{ width: 150 }} />
                    </Form.Item>
                    <Form.Item name={[index, 'type']}>
                      <Select options={['string', 'number', 'ip', 'mac', 'duration'].map(t => ({ label: t, value: t }))} style={{ width: 120 }} />
                    </Form.Item>
                    <Button type="link" danger onClick={() => remove(index)}>{t('common.remove')}</Button>
                  </div>
                ))}
                <Button type="dashed" onClick={() => add({ name: '', label: '', type: 'string' })} block>{t('parsers.addField')}</Button>
              </>
            )}
          </Form.List>
        </Form>
      </Modal>

      <Modal
        title={t('parsers.testTitle')}
        open={testModalOpen}
        onCancel={() => { setTestModalOpen(false); setTestResult(null) }}
        footer={null}
        width={{ sm: '95%', md: 800 }}
      >
        <Form layout="vertical">
          <Form.Item label={t('parsers.regexPattern')}>
            <Input.TextArea rows={2} value={pattern} onChange={e => setPattern(e.target.value)} placeholder='(?P<action>\w+) (?P<ip>\d+\.\d+\.\d+\.\d+)' />
          </Form.Item>
          <Form.Item label={t('parsers.sampleLog')}>
            <Input.TextArea rows={4} value={sampleLog} onChange={e => setSampleLog(e.target.value)} placeholder={t('parsers.pasteLogLine')} />
          </Form.Item>
          <Button type="primary" icon={<PlayCircleOutlined />} onClick={handleTest} loading={testLoading} block>{t('parsers.test')}</Button>
        </Form>
        {testResult && (
          <Card
            style={{ marginTop: 16 }}
            type="inner"
            title={testResult.matched ? t('parsers.matched') : t('parsers.noMatch')}
          >
            {testResult.parser_name && <p><Text strong>{t('parsers.parser')}:</Text> {testResult.parser_name}</p>}
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
