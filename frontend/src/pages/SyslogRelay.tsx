import { useState, useEffect } from 'react'
import { Card, Table, Button, Modal, Form, Input, Space, Tag, message, Tabs, Popconfirm, Alert, Typography } from 'antd'
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
      message.error('Failed to load relay whitelist')
    } finally {
      setWhitelistLoading(false)
    }
  }

  const loadCerts = async () => {
    setCertsLoading(true)
    try {
      setCerts(await getRelayCertificates())
    } catch {
      message.error('Failed to load relay certificates')
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
      message.success('Whitelist entry added')
      setWhitelistModalOpen(false)
      whitelistForm.resetFields()
      loadWhitelist()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to add whitelist entry'))
    } finally {
      setWhitelistSaving(false)
    }
  }

  const handleDeleteWhitelist = async (id: number) => {
    try {
      await deleteRelayWhitelistEntry(id)
      message.success('Whitelist entry removed')
      loadWhitelist()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to remove whitelist entry'))
    }
  }

  const warnSaveNow = (filename: string) => {
    Modal.warning({
      title: 'Save this file now',
      width: 520,
      content: (
        <div>
          <Paragraph>
            Downloaded <Text code>{filename}</Text>. It contains the relay's private key (<Text code>client.key</Text>) -
            the server doesn't store it, and <Text strong>it cannot be downloaded again</Text>.
          </Paragraph>
          <Paragraph>
            If you lose this file, the only option is to revoke this certificate and generate a new one.
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
      message.error(getErrorMessage(e, 'Failed to generate certificate'))
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
      message.error(getErrorMessage(e, 'Failed to generate certificate'))
    } finally {
      setGeneratingForWhitelistId(null)
    }
  }

  const handleRevokeCert = async (id: number) => {
    try {
      await revokeRelayCertificate(id)
      message.success('Certificate revoked')
      loadCerts()
      loadWhitelist()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to revoke certificate'))
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
      message.error(getErrorMessage(e, 'Failed to regenerate certificate'))
    } finally {
      setRegeneratingCertId(null)
    }
  }

  const linkedCert = (record: RelayWhitelistEntry) =>
    record.relay_cert_id ? certs.find(c => c.id === record.relay_cert_id) : undefined

  const daysUntil = (isoDate: string) => Math.ceil((new Date(isoDate).getTime() - Date.now()) / (24 * 60 * 60 * 1000))
  const isNearingExpiry = (record: RelayCertificate) => daysUntil(record.expires_at) <= RELAY_CERT_RENEWAL_WINDOW_DAYS

  const whitelistColumns = [
    { title: 'IP', dataIndex: 'ip_address', key: 'ip_address' },
    { title: 'Label', dataIndex: 'label', key: 'label' },
    {
      title: 'Access',
      key: 'access',
      render: (_v: unknown, record: RelayWhitelistEntry) => {
        if (!record.relay_cert_id) return <Tag>No certificate</Tag>
        const cert = linkedCert(record)
        if (cert?.status === 'revoked') {
          return <Tag color="red">Blocked (certificate #{record.relay_cert_id} revoked)</Tag>
        }
        return <Tag color="green">Active (certificate #{record.relay_cert_id})</Tag>
      },
    },
    {
      title: 'Added',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (date: string) => new Date(date).toLocaleString(),
    },
    {
      title: 'Actions',
      key: 'actions',
      render: (_v: unknown, record: RelayWhitelistEntry) => {
        const cert = linkedCert(record)
        const canGenerate = !record.relay_cert_id || cert?.status === 'revoked'
        return (
          <Space>
            {canGenerate && (
              <Popconfirm
                title="Generate a certificate for this entry?"
                description="Downloads a one-time bundle (ca.crt, client.crt, client.key, relay.conf) for this IP - save it, it can't be fetched again."
                okText="Yes, generate"
                cancelText="No"
                onConfirm={() => handleGenerateCertForWhitelistEntry(record)}
              >
                <Button size="small" icon={<SafetyCertificateOutlined />} loading={generatingForWhitelistId === record.id}>
                  Generate Certificate
                </Button>
              </Popconfirm>
            )}
            <Popconfirm
              title="Remove this whitelist entry?"
              description="If it has a certificate, that certificate will be revoked too."
              okText="Yes"
              cancelText="No"
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
    { title: 'Label', dataIndex: 'label', key: 'label' },
    {
      title: 'Status',
      dataIndex: 'status',
      key: 'status',
      render: (status: string) => <Tag color={status === 'issued' ? 'green' : 'red'}>{status === 'issued' ? 'issued' : 'revoked'}</Tag>,
    },
    {
      title: 'Fingerprint (SHA-256)',
      dataIndex: 'fingerprint_sha256',
      key: 'fingerprint_sha256',
      render: (fp: string) => <Text code style={{ fontSize: 12 }}>{fp.slice(0, 16)}…</Text>,
    },
    {
      title: 'Issued',
      dataIndex: 'issued_at',
      key: 'issued_at',
      render: (date: string) => new Date(date).toLocaleString(),
    },
    {
      title: 'Expires',
      dataIndex: 'expires_at',
      key: 'expires_at',
      render: (date: string, record: RelayCertificate) => {
        const days = daysUntil(date)
        const formatted = new Date(date).toLocaleDateString()
        if (record.status !== 'issued') return <Text type="secondary">{formatted}</Text>
        if (days <= 0) return <Space><Text>{formatted}</Text><Tag color="red">Expired</Tag></Space>
        if (days <= RELAY_CERT_RENEWAL_WINDOW_DAYS) return <Space><Text>{formatted}</Text><Tag color="orange">Expires in {days}d</Tag></Space>
        return formatted
      },
    },
    {
      title: 'Actions',
      key: 'actions',
      render: (_v: unknown, record: RelayCertificate) => {
        if (record.status !== 'issued') {
          return (
            <Popconfirm
              title="Regenerate this certificate?"
              description="Issues a new certificate for the same whitelist entry and downloads it once. This revoked one stays in the list."
              okText="Yes, regenerate"
              cancelText="No"
              onConfirm={() => handleRegenerateCert(record)}
            >
              <Button size="small" icon={<SafetyCertificateOutlined />} loading={regeneratingCertId === record.id}>
                Regenerate
              </Button>
            </Popconfirm>
          )
        }
        return (
          <Space>
            {isNearingExpiry(record) && (
              <Popconfirm
                title="Renew this certificate?"
                description="Issues a replacement for the same whitelist entry and downloads it once - this certificate is revoked as soon as the new one is linked."
                okText="Yes, renew"
                cancelText="No"
                onConfirm={() => handleRegenerateCert(record)}
              >
                <Button size="small" type="primary" icon={<SafetyCertificateOutlined />} loading={regeneratingCertId === record.id}>
                  Renew
                </Button>
              </Popconfirm>
            )}
            <Popconfirm
              title="Revoke this certificate?"
              description="Blocks this relay immediately - its IP is dropped from the active allow-list. The device stays listed (shown as Blocked) so you can generate a new certificate for it whenever you're ready."
              okText="Yes, revoke"
              cancelText="No"
              onConfirm={() => handleRevokeCert(record.id)}
            >
              <Button size="small" danger>Revoke</Button>
            </Popconfirm>
          </Space>
        )
      },
    },
  ]

  return (
    <div>
      <Card style={{ marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>Syslog Relay</h2>
        <Text type="secondary">Manage syslog relays for multi-VLAN deployments (mTLS + IP whitelist). Central server address is set under Admin &gt; Settings &gt; Syslog Relay.</Text>
      </Card>

      <Tabs
        defaultActiveKey="whitelist"
        items={[
          {
            key: 'whitelist',
            label: 'Whitelist IP',
            children: (
              <Card
                title="Allowed relay IP addresses"
                extra={<Button type="primary" icon={<PlusOutlined />} onClick={() => setWhitelistModalOpen(true)}>Add IP</Button>}
              >
                <Alert
                  style={{ marginBottom: 16 }}
                  type="info"
                  showIcon
                  message="Only connections from an IP whose current certificate is Active are accepted on the mTLS port (6514) - everything else, including a Blocked entry's old (revoked) key, is dropped. Removing an entry here revokes its certificate too."
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
            label: 'Certificates',
            children: (
              <Card
                title="Relay certificates"
                extra={<Button type="primary" icon={<SafetyCertificateOutlined />} onClick={() => setCertModalOpen(true)}>Generate Certificate</Button>}
              >
                <Alert
                  style={{ marginBottom: 16 }}
                  type="warning"
                  showIcon
                  message="The private key is only ever shown once, at generation time. The server doesn't store it - if it's lost, the only option is to revoke the certificate and generate a new one."
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
        title="Add IP to whitelist"
        open={whitelistModalOpen}
        onOk={handleAddWhitelist}
        onCancel={() => { setWhitelistModalOpen(false); whitelistForm.resetFields() }}
        confirmLoading={whitelistSaving}
        okText="Add"
        cancelText="Cancel"
      >
        <Form form={whitelistForm} layout="vertical">
          <Form.Item label="Relay IP address" name="ip_address" rules={[{ required: true, message: 'Required' }]}>
            <Input placeholder="e.g. 10.20.0.10" />
          </Form.Item>
          <Form.Item label="Label" name="label" rules={[{ required: true, message: 'Required' }]}>
            <Input placeholder="e.g. relay-vlan-b" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="Generate relay certificate"
        open={certModalOpen}
        onOk={handleGenerateCert}
        onCancel={() => { setCertModalOpen(false); certForm.resetFields() }}
        confirmLoading={certGenerating}
        okText="Generate & Download"
        cancelText="Cancel"
        okButtonProps={{ icon: <DownloadOutlined /> }}
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          <Alert
            type="info"
            showIcon
            message="This will generate a client certificate signed by this server's CA and add the given IP address to the whitelist. The bundle (ca.crt, client.crt, client.key, relay.conf) downloads automatically - this one time only."
          />
          <Form form={certForm} layout="vertical" style={{ width: '100%' }}>
            <Form.Item label="Relay label" name="label" rules={[{ required: true, message: 'Required' }]}>
              <Input placeholder="e.g. relay-vlan-b" />
            </Form.Item>
            <Form.Item label="Relay IP address" name="ip_address" rules={[{ required: true, message: 'Required' }]}>
              <Input placeholder="e.g. 10.20.0.10" />
            </Form.Item>
          </Form>
        </Space>
      </Modal>
    </div>
  )
}
