import { useState, useEffect } from 'react'
import { Card, Table, Button, Modal, Form, Input, Space, Tag, message, Tabs, Popconfirm, Alert, Typography } from 'antd'
import { PlusOutlined, DeleteOutlined, SafetyCertificateOutlined, DownloadOutlined } from '@ant-design/icons'
import {
  getRelayWhitelist, addRelayWhitelistEntry, deleteRelayWhitelistEntry,
  getRelayCertificates, generateRelayCertificate, revokeRelayCertificate,
  RelayWhitelistEntry, RelayCertificate,
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

  const handleGenerateCert = async () => {
    const values = await certForm.validateFields()
    setCertGenerating(true)
    try {
      const { filename } = await generateRelayCertificate(values)
      setCertModalOpen(false)
      certForm.resetFields()
      Modal.warning({
        title: 'Zapisz ten plik teraz',
        width: 520,
        content: (
          <div>
            <Paragraph>
              Pobrano <Text code>{filename}</Text>. Zawiera klucz prywatny relaya (<Text code>client.key</Text>) -
              serwer go nie przechowuje i <Text strong>nie da się go pobrać ponownie</Text>.
            </Paragraph>
            <Paragraph>
              Jeśli zgubisz ten plik, jedyną opcją jest odwołanie tego certyfikatu i wygenerowanie nowego.
            </Paragraph>
          </div>
        ),
      })
      loadCerts()
      loadWhitelist()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to generate certificate'))
    } finally {
      setCertGenerating(false)
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

  const whitelistColumns = [
    { title: 'IP', dataIndex: 'ip_address', key: 'ip_address' },
    { title: 'Etykieta', dataIndex: 'label', key: 'label' },
    {
      title: 'Powiązany certyfikat',
      dataIndex: 'relay_cert_id',
      key: 'relay_cert_id',
      render: (id?: number) => id ? <Tag color="blue">#{id}</Tag> : <Text type="secondary">brak</Text>,
    },
    {
      title: 'Dodano',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (date: string) => new Date(date).toLocaleString(),
    },
    {
      title: 'Akcje',
      key: 'actions',
      render: (_v: unknown, record: RelayWhitelistEntry) => (
        <Popconfirm title="Usunąć wpis z whitelisty?" okText="Tak" cancelText="Nie" onConfirm={() => handleDeleteWhitelist(record.id)}>
          <Button size="small" danger icon={<DeleteOutlined />} />
        </Popconfirm>
      ),
    },
  ]

  const certColumns = [
    { title: 'Etykieta', dataIndex: 'label', key: 'label' },
    {
      title: 'Status',
      dataIndex: 'status',
      key: 'status',
      render: (status: string) => <Tag color={status === 'issued' ? 'green' : 'red'}>{status === 'issued' ? 'wydany' : 'odwołany'}</Tag>,
    },
    {
      title: 'Odcisk (SHA-256)',
      dataIndex: 'fingerprint_sha256',
      key: 'fingerprint_sha256',
      render: (fp: string) => <Text code style={{ fontSize: 12 }}>{fp.slice(0, 16)}…</Text>,
    },
    {
      title: 'Wydano',
      dataIndex: 'issued_at',
      key: 'issued_at',
      render: (date: string) => new Date(date).toLocaleString(),
    },
    {
      title: 'Akcje',
      key: 'actions',
      render: (_v: unknown, record: RelayCertificate) => (
        record.status === 'issued' ? (
          <Popconfirm
            title="Odwołać ten certyfikat?"
            description="Powiązany wpis whitelisty zostanie usunięty - relay straci dostęp natychmiast."
            okText="Tak, odwołaj"
            cancelText="Nie"
            onConfirm={() => handleRevokeCert(record.id)}
          >
            <Button size="small" danger>Odwołaj</Button>
          </Popconfirm>
        ) : <Text type="secondary">-</Text>
      ),
    },
  ]

  return (
    <div>
      <Card style={{ marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>Syslog Relay</h2>
        <Text type="secondary">Zarządzanie relayami syslog dla wdrożeń wielo-VLAN (mTLS + whitelist IP)</Text>
      </Card>

      <Tabs
        defaultActiveKey="whitelist"
        items={[
          {
            key: 'whitelist',
            label: 'Whitelist IP',
            children: (
              <Card
                title="Dozwolone adresy IP relayów"
                extra={<Button type="primary" icon={<PlusOutlined />} onClick={() => setWhitelistModalOpen(true)}>Dodaj IP</Button>}
              >
                <Alert
                  style={{ marginBottom: 16 }}
                  type="info"
                  showIcon
                  message="Tylko połączenia z adresu IP na tej liście, prezentujące certyfikat podpisany przez CA tego serwera, są akceptowane na porcie mTLS (6514). Wszystko inne jest odrzucane."
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
            label: 'Certyfikaty',
            children: (
              <Card
                title="Certyfikaty relayów"
                extra={<Button type="primary" icon={<SafetyCertificateOutlined />} onClick={() => setCertModalOpen(true)}>Generuj certyfikat</Button>}
              >
                <Alert
                  style={{ marginBottom: 16 }}
                  type="warning"
                  showIcon
                  message="Klucz prywatny jest widoczny tylko raz, w chwili generowania. Serwer go nie przechowuje - w razie zgubienia jedyną opcją jest odwołanie certyfikatu i wygenerowanie nowego."
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
        title="Dodaj IP do whitelisty"
        open={whitelistModalOpen}
        onOk={handleAddWhitelist}
        onCancel={() => { setWhitelistModalOpen(false); whitelistForm.resetFields() }}
        confirmLoading={whitelistSaving}
        okText="Dodaj"
        cancelText="Anuluj"
      >
        <Form form={whitelistForm} layout="vertical">
          <Form.Item label="Adres IP relaya" name="ip_address" rules={[{ required: true, message: 'Wymagane' }]}>
            <Input placeholder="np. 10.20.0.10" />
          </Form.Item>
          <Form.Item label="Etykieta" name="label" rules={[{ required: true, message: 'Wymagane' }]}>
            <Input placeholder="np. relay-vlan-b" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="Generuj certyfikat relaya"
        open={certModalOpen}
        onOk={handleGenerateCert}
        onCancel={() => { setCertModalOpen(false); certForm.resetFields() }}
        confirmLoading={certGenerating}
        okText="Generuj i pobierz"
        cancelText="Anuluj"
        okButtonProps={{ icon: <DownloadOutlined /> }}
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          <Alert
            type="info"
            showIcon
            message="Wygeneruje certyfikat kliencki podpisany przez CA tego serwera oraz doda podany adres IP do whitelisty. Paczka (ca.crt, client.crt, client.key, relay.conf) zostanie pobrana automatycznie - tylko ten jeden raz."
          />
          <Form form={certForm} layout="vertical" style={{ width: '100%' }}>
            <Form.Item label="Etykieta relaya" name="label" rules={[{ required: true, message: 'Wymagane' }]}>
              <Input placeholder="np. relay-vlan-b" />
            </Form.Item>
            <Form.Item label="Adres IP relaya" name="ip_address" rules={[{ required: true, message: 'Wymagane' }]}>
              <Input placeholder="np. 10.20.0.10" />
            </Form.Item>
          </Form>
        </Space>
      </Modal>
    </div>
  )
}
