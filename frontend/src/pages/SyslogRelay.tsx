import { useState, useEffect } from 'react'
import { Card, Table, Button, Modal, Form, Input, Space, Tag, message, Tabs, Popconfirm, Alert, Typography } from 'antd'
import { Trans, useTranslation } from 'react-i18next'
import { PlusOutlined, DeleteOutlined, SafetyCertificateOutlined, DownloadOutlined } from '@ant-design/icons'
import {
  getRelayWhitelist, addRelayWhitelistEntry, deleteRelayWhitelistEntry,
  getRelayCertificates, generateRelayCertificate, generateRelayCertificateForWhitelistEntry,
  revokeRelayCertificate, regenerateRelayCertificate,
  RelayWhitelistEntry, RelayCertificate, RELAY_CERT_RENEWAL_WINDOW_DAYS,
} from '../services/api'
import { getErrorMessage } from '../utils/error'

const { Text, Paragraph } = Typography

export default function SyslogRelay() {
  const { t } = useTranslation()
  const [whitelist, setWhitelist] = useState<RelayWhitelistEntry[]>([])
  const [whitelistLoading, setWhitelistLoading] = useState(false)
  const [whitelistModalOpen, setWhitelistModalOpen] = useState(false)
  const [whitelistForm] = Form.useForm()
  const [whitelistSaving, setWhitelistSaving] = useState(false)

  const [certs, setCerts] = useState<RelayCertificate[]>([])
  const [certsLoading, setCertsLoading] = useState(false)
  const [certModalOpen, setCertModalOpen] = useState(false)
  const [certForm] = Form.useForm()
  const [certGenerating, setCertGenerating] = useState(false)
  const [generatingForWhitelistId, setGeneratingForWhitelistId] = useState<number | null>(null)
  const [regeneratingCertId, setRegeneratingCertId] = useState<number | null>(null)

  const loadWhitelist = async () => {
    setWhitelistLoading(true)
    try {
      setWhitelist(await getRelayWhitelist())
    } catch {
      message.error(t('relay.loadWhitelistFailed'))
    } finally {
      setWhitelistLoading(false)
    }
  }

  const loadCerts = async () => {
    setCertsLoading(true)
    try {
      setCerts(await getRelayCertificates())
    } catch {
      message.error(t('relay.loadCertsFailed'))
    } finally {
      setCertsLoading(false)
    }
  }

  useEffect(() => {
    loadWhitelist()
    loadCerts()
  }, [])

  const handleAddWhitelist = async () => {
    const values = await whitelistForm.validateFields()
    setWhitelistSaving(true)
    try {
      await addRelayWhitelistEntry(values)
      message.success(t('relay.whitelistAdded'))
      setWhitelistModalOpen(false)
      whitelistForm.resetFields()
      loadWhitelist()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('relay.addWhitelistFailed')))
    } finally {
      setWhitelistSaving(false)
    }
  }

  const handleDeleteWhitelist = async (id: number) => {
    try {
      await deleteRelayWhitelistEntry(id)
      message.success(t('relay.whitelistRemoved'))
      loadWhitelist()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('relay.removeWhitelistFailed')))
    }
  }

  const warnSaveNow = (filename: string) => {
    Modal.warning({
      title: t('relay.saveFileNowTitle'),
      width: 520,
      content: (
        <div>
          <Paragraph>
            <Trans
              i18nKey="relay.saveFileNowP1"
              values={{ filename }}
              components={{ code: <Text code />, strong: <Text strong /> }}
            />
          </Paragraph>
          <Paragraph>
            {t('relay.saveFileNowP2')}
          </Paragraph>
        </div>
      ),
    })
  }

  const handleGenerateCert = async () => {
    const values = await certForm.validateFields()
    setCertGenerating(true)
    try {
      const { filename } = await generateRelayCertificate(values)
      setCertModalOpen(false)
      certForm.resetFields()
      warnSaveNow(filename)
      loadCerts()
      loadWhitelist()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('relay.generateCertFailed')))
    } finally {
      setCertGenerating(false)
    }
  }

  const handleGenerateCertForWhitelistEntry = async (entry: RelayWhitelistEntry) => {
    setGeneratingForWhitelistId(entry.id)
    try {
      const { filename } = await generateRelayCertificateForWhitelistEntry(entry.id, entry.label)
      warnSaveNow(filename)
      loadCerts()
      loadWhitelist()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('relay.generateCertFailed')))
    } finally {
      setGeneratingForWhitelistId(null)
    }
  }

  const handleRevokeCert = async (id: number) => {
    try {
      await revokeRelayCertificate(id)
      message.success(t('relay.certRevoked'))
      loadCerts()
      loadWhitelist()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('relay.revokeCertFailed')))
    }
  }

  const handleRegenerateCert = async (record: RelayCertificate) => {
    setRegeneratingCertId(record.id)
    try {
      const { filename } = await regenerateRelayCertificate(record.id, record.label)
      warnSaveNow(filename)
      loadCerts()
      loadWhitelist()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('relay.regenerateCertFailed')))
    } finally {
      setRegeneratingCertId(null)
    }
  }

  const linkedCert = (record: RelayWhitelistEntry) =>
    record.relay_cert_id ? certs.find(c => c.id === record.relay_cert_id) : undefined

  const daysUntil = (isoDate: string) => Math.ceil((new Date(isoDate).getTime() - Date.now()) / (24 * 60 * 60 * 1000))
  const isNearingExpiry = (record: RelayCertificate) => daysUntil(record.expires_at) <= RELAY_CERT_RENEWAL_WINDOW_DAYS

  const whitelistColumns = [
    { title: t('relay.ip'), dataIndex: 'ip_address', key: 'ip_address' },
    { title: t('common.label'), dataIndex: 'label', key: 'label' },
    {
      title: t('relay.access'),
      key: 'access',
      render: (_v: unknown, record: RelayWhitelistEntry) => {
        if (!record.relay_cert_id) return <Tag>{t('relay.noCertificate')}</Tag>
        const cert = linkedCert(record)
        if (cert?.status === 'revoked') {
          return <Tag color="red">{t('relay.blocked', { id: record.relay_cert_id })}</Tag>
        }
        return <Tag color="green">{t('relay.active', { id: record.relay_cert_id })}</Tag>
      },
    },
    {
      title: t('relay.added'),
      dataIndex: 'created_at',
      key: 'created_at',
      render: (date: string) => new Date(date).toLocaleString(),
    },
    {
      title: t('common.actions'),
      key: 'actions',
      render: (_v: unknown, record: RelayWhitelistEntry) => {
        const cert = linkedCert(record)
        const canGenerate = !record.relay_cert_id || cert?.status === 'revoked'
        return (
          <Space>
            {canGenerate && (
              <Popconfirm
                title={t('relay.generateCertConfirmTitle')}
                description={t('relay.generateCertConfirmDesc')}
                okText={t('relay.yesGenerate')}
                cancelText={t('common.no')}
                onConfirm={() => handleGenerateCertForWhitelistEntry(record)}
              >
                <Button size="small" icon={<SafetyCertificateOutlined />} loading={generatingForWhitelistId === record.id}>
                  {t('relay.generateCertificate')}
                </Button>
              </Popconfirm>
            )}
            <Popconfirm
              title={t('relay.removeConfirmTitle')}
              description={t('relay.removeConfirmDesc')}
              okText={t('common.yes')}
              cancelText={t('common.no')}
              onConfirm={() => handleDeleteWhitelist(record.id)}
            >
              <Button size="small" danger icon={<DeleteOutlined />} />
            </Popconfirm>
          </Space>
        )
      },
    },
  ]

  const certColumns = [
    { title: t('common.label'), dataIndex: 'label', key: 'label' },
    {
      title: t('common.status'),
      dataIndex: 'status',
      key: 'status',
      render: (status: string) => <Tag color={status === 'issued' ? 'green' : 'red'}>{status === 'issued' ? t('relay.issuedStatus') : t('relay.revokedStatus')}</Tag>,
    },
    {
      title: t('relay.fingerprint'),
      dataIndex: 'fingerprint_sha256',
      key: 'fingerprint_sha256',
      render: (fp: string) => <Text code style={{ fontSize: 12 }}>{fp.slice(0, 16)}…</Text>,
    },
    {
      title: t('relay.issued'),
      dataIndex: 'issued_at',
      key: 'issued_at',
      render: (date: string) => new Date(date).toLocaleString(),
    },
    {
      title: t('relay.expires'),
      dataIndex: 'expires_at',
      key: 'expires_at',
      render: (date: string, record: RelayCertificate) => {
        const days = daysUntil(date)
        const formatted = new Date(date).toLocaleDateString()
        if (record.status !== 'issued') return <Text type="secondary">{formatted}</Text>
        if (days <= 0) return <Space><Text>{formatted}</Text><Tag color="red">{t('relay.expired')}</Tag></Space>
        if (days <= RELAY_CERT_RENEWAL_WINDOW_DAYS) return <Space><Text>{formatted}</Text><Tag color="orange">{t('relay.expiresIn', { days })}</Tag></Space>
        return formatted
      },
    },
    {
      title: t('common.actions'),
      key: 'actions',
      render: (_v: unknown, record: RelayCertificate) => {
        if (record.status !== 'issued') {
          return (
            <Popconfirm
              title={t('relay.regenerateConfirmTitle')}
              description={t('relay.regenerateConfirmDesc')}
              okText={t('relay.yesRegenerate')}
              cancelText={t('common.no')}
              onConfirm={() => handleRegenerateCert(record)}
            >
              <Button size="small" icon={<SafetyCertificateOutlined />} loading={regeneratingCertId === record.id}>
                {t('relay.regenerate')}
              </Button>
            </Popconfirm>
          )
        }
        return (
          <Space>
            {isNearingExpiry(record) && (
              <Popconfirm
                title={t('relay.renewConfirmTitle')}
                description={t('relay.renewConfirmDesc')}
                okText={t('relay.yesRenew')}
                cancelText={t('common.no')}
                onConfirm={() => handleRegenerateCert(record)}
              >
                <Button size="small" type="primary" icon={<SafetyCertificateOutlined />} loading={regeneratingCertId === record.id}>
                  {t('relay.renew')}
                </Button>
              </Popconfirm>
            )}
            <Popconfirm
              title={t('relay.revokeConfirmTitle')}
              description={t('relay.revokeConfirmDesc')}
              okText={t('relay.yesRevoke')}
              cancelText={t('common.no')}
              onConfirm={() => handleRevokeCert(record.id)}
            >
              <Button size="small" danger>{t('relay.revoke')}</Button>
            </Popconfirm>
          </Space>
        )
      },
    },
  ]

  return (
    <div>
      <Card style={{ marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>{t('nav.relay')}</h2>
        <Text type="secondary">{t('relay.description')}</Text>
      </Card>

      <Tabs
        defaultActiveKey="whitelist"
        items={[
          {
            key: 'whitelist',
            label: t('relay.whitelistTab'),
            children: (
              <Card
                title={t('relay.allowedIps')}
                extra={<Button type="primary" icon={<PlusOutlined />} onClick={() => setWhitelistModalOpen(true)}>{t('relay.addIp')}</Button>}
              >
                <Alert
                  style={{ marginBottom: 16 }}
                  type="info"
                  showIcon
                  message={t('relay.whitelistInfo')}
                />
                <Table
                  rowKey="id"
                  loading={whitelistLoading}
                  dataSource={whitelist}
                  columns={whitelistColumns}
                  pagination={false}
                />
              </Card>
            ),
          },
          {
            key: 'certificates',
            label: t('relay.certificatesTab'),
            children: (
              <Card
                title={t('relay.relayCertificates')}
                extra={<Button type="primary" icon={<SafetyCertificateOutlined />} onClick={() => setCertModalOpen(true)}>{t('relay.generateCertificate')}</Button>}
              >
                <Alert
                  style={{ marginBottom: 16 }}
                  type="warning"
                  showIcon
                  message={t('relay.certInfo')}
                />
                <Table
                  rowKey="id"
                  loading={certsLoading}
                  dataSource={certs}
                  columns={certColumns}
                  pagination={false}
                />
              </Card>
            ),
          },
        ]}
      />

      <Modal
        title={t('relay.addToWhitelist')}
        open={whitelistModalOpen}
        onOk={handleAddWhitelist}
        onCancel={() => { setWhitelistModalOpen(false); whitelistForm.resetFields() }}
        confirmLoading={whitelistSaving}
        okText={t('relay.add')}
        cancelText={t('common.cancel')}
      >
        <Form form={whitelistForm} layout="vertical">
          <Form.Item label={t('relay.relayIpAddress')} name="ip_address" rules={[{ required: true, message: t('relay.required') }]}>
            <Input placeholder={t('relay.ipPlaceholder')} />
          </Form.Item>
          <Form.Item label={t('common.label')} name="label" rules={[{ required: true, message: t('relay.required') }]}>
            <Input placeholder={t('relay.labelPlaceholder')} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={t('relay.generateRelayCertificate')}
        open={certModalOpen}
        onOk={handleGenerateCert}
        onCancel={() => { setCertModalOpen(false); certForm.resetFields() }}
        confirmLoading={certGenerating}
        okText={t('relay.generateAndDownload')}
        cancelText={t('common.cancel')}
        okButtonProps={{ icon: <DownloadOutlined /> }}
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          <Alert
            type="info"
            showIcon
            message={t('relay.generateInfo')}
          />
          <Form form={certForm} layout="vertical" style={{ width: '100%' }}>
            <Form.Item label={t('relay.relayLabel')} name="label" rules={[{ required: true, message: t('relay.required') }]}>
              <Input placeholder={t('relay.labelPlaceholder')} />
            </Form.Item>
            <Form.Item label={t('relay.relayIpAddress')} name="ip_address" rules={[{ required: true, message: t('relay.required') }]}>
              <Input placeholder={t('relay.ipPlaceholder')} />
            </Form.Item>
          </Form>
        </Space>
      </Modal>
    </div>
  )
}
