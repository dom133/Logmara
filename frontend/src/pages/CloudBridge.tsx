import { useEffect, useState } from 'react'
import { Card, Form, Input, Button, Tag, Typography, message, Descriptions, FormInstance, Popconfirm, Space } from 'antd'
import { useTranslation } from 'react-i18next'
import { CloudOutlined, CheckCircleOutlined, CloseCircleOutlined, UploadOutlined, DisconnectOutlined } from '@ant-design/icons'
import {
  getCloudBridgeStatus,
  submitCloudBridgeLink,
  saveCloudBridgeCertificates,
  disconnectCloudBridge,
  CloudBridgeStatus,
} from '../services/api'
import { getErrorMessage } from '../utils/error'

const { Text, Paragraph } = Typography

// Polls while the tab is open so "connected" reflects the tunnel's real
// state (it can flip without any action here - the agent reconnects on
// its own) instead of only updating on the next full page load.
const STATUS_POLL_MS = 5000

interface CertFormValues {
  ca_cert: string
  client_cert: string
  client_key: string
}

// Paste box + "load from file" button for one PEM field - same pattern as
// the LDAP CA cert field in Admin.tsx (Input.TextArea bound to the form,
// a hidden native file input reads the chosen file via FileReader and
// pushes the text into the same field), repeated for the three cert fields
// this page needs instead of just the one Admin.tsx has.
function PemField({
  form,
  name,
  label,
}: {
  form: FormInstance<CertFormValues>
  name: keyof CertFormValues
  label: string
}) {
  const { t } = useTranslation()
  const inputId = `cloud-bridge-cert-upload-${name}`
  return (
    <>
      <Form.Item
        name={name}
        label={label}
        rules={[{ required: true, message: t('cloudBridge.certificatesRequired') }]}
      >
        <Input.TextArea rows={4} placeholder="-----BEGIN CERTIFICATE-----..." style={{ resize: 'none' }} />
      </Form.Item>
      <Form.Item>
        <input
          type="file"
          accept=".pem,.crt,.cer,.key"
          style={{ display: 'none' }}
          id={inputId}
          onChange={(e) => {
            const file = e.target.files?.[0]
            if (!file) return
            const reader = new FileReader()
            reader.onload = (ev) => {
              const text = ev.target?.result
              if (typeof text === 'string') {
                form.setFieldValue(name, text)
                message.success(t('cloudBridge.pemLoaded'))
              }
            }
            reader.onerror = () => message.error(t('cloudBridge.pemReadFailed'))
            reader.readAsText(file)
            e.target.value = ''
          }}
        />
        <Button icon={<UploadOutlined />} block onClick={() => document.getElementById(inputId)?.click()}>
          {t('cloudBridge.uploadPem')}
        </Button>
      </Form.Item>
    </>
  )
}

export default function CloudBridge() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<CloudBridgeStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [form] = Form.useForm()
  const [submitting, setSubmitting] = useState(false)
  const [certForm] = Form.useForm<CertFormValues>()
  const [savingCerts, setSavingCerts] = useState(false)
  const [editingCerts, setEditingCerts] = useState(false)
  const [disconnecting, setDisconnecting] = useState(false)

  const loadStatus = async () => {
    try {
      setStatus(await getCloudBridgeStatus())
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('cloudBridge.loadStatusFailed')))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadStatus()
    const interval = setInterval(loadStatus, STATUS_POLL_MS)
    return () => clearInterval(interval)
  }, [])

  const handleSubmit = async () => {
    const { link } = await form.validateFields()
    setSubmitting(true)
    try {
      const result = await submitCloudBridgeLink(link)
      message.success(t('cloudBridge.pairSuccess'))
      form.resetFields()
      // Empty when certificates are locked (CLOUD_BRIDGE_LOCK_CERTIFICATES) -
      // the backend already saved and connected them itself in that case,
      // so there's nothing here to pre-fill for review.
      if (result.ca_cert || result.client_cert || result.client_key) {
        certForm.setFieldsValue({
          ca_cert: result.ca_cert,
          client_cert: result.client_cert,
          client_key: result.client_key,
        })
      }
      await loadStatus()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('cloudBridge.pairFailed')))
    } finally {
      setSubmitting(false)
    }
  }

  const handleSaveCertificates = async () => {
    const values = await certForm.validateFields()
    setSavingCerts(true)
    try {
      await saveCloudBridgeCertificates(values)
      message.success(t('cloudBridge.certificatesSaved'))
      certForm.resetFields()
      setEditingCerts(false)
      await loadStatus()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('cloudBridge.certificatesSaveFailed')))
    } finally {
      setSavingCerts(false)
    }
  }

  const handleDisconnect = async () => {
    setDisconnecting(true)
    try {
      await disconnectCloudBridge()
      message.success(t('cloudBridge.disconnectSuccess'))
      certForm.resetFields()
      setEditingCerts(false)
      await loadStatus()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('cloudBridge.disconnectFailed')))
    } finally {
      setDisconnecting(false)
    }
  }

  const showCertForm =
    !!status?.enrolled && !status.certificates_locked && (!status.certificates_configured || editingCerts)

  return (
    <div style={{ maxWidth: 640 }}>
      <Card
        title={
          <span>
            <CloudOutlined style={{ marginRight: 8 }} />
            {t('cloudBridge.title')}
          </span>
        }
        loading={loading}
      >
        {!status?.enrolled ? (
          <>
            <Paragraph type="secondary">{t('cloudBridge.pairIntro')}</Paragraph>
            <Form form={form} layout="vertical" onFinish={handleSubmit}>
              <Form.Item
                name="link"
                label={t('cloudBridge.linkLabel')}
                rules={[{ required: true, message: t('cloudBridge.linkRequired') }]}
              >
                <Input placeholder="https://cloud.example.com/broker/enroll?token=..." />
              </Form.Item>
              <Button type="primary" htmlType="submit" loading={submitting}>
                {t('cloudBridge.pairButton')}
              </Button>
            </Form>
          </>
        ) : (
          <>
            <Descriptions column={1} bordered size="small">
              <Descriptions.Item label={t('cloudBridge.instanceId')}>
                <Text code copyable>
                  {status.instance_id}
                </Text>
              </Descriptions.Item>
              <Descriptions.Item label={t('cloudBridge.status')}>
                {!status.certificates_configured ? (
                  <Tag>{t('cloudBridge.certificatesNotConfigured')}</Tag>
                ) : status.connected ? (
                  <Tag icon={<CheckCircleOutlined />} color="success">
                    {t('cloudBridge.connected')}
                  </Tag>
                ) : (
                  <Tag icon={<CloseCircleOutlined />} color="error">
                    {t('cloudBridge.disconnected')}
                  </Tag>
                )}
              </Descriptions.Item>
              {status.enrolled_at && (
                <Descriptions.Item label={t('cloudBridge.enrolledAt')}>
                  {new Date(status.enrolled_at).toLocaleString()}
                </Descriptions.Item>
              )}
            </Descriptions>

            {status.certificates_locked && !status.certificates_configured && (
              <Paragraph type="warning" style={{ marginTop: 16 }}>
                {t('cloudBridge.certificatesLockedNotConfigured')}
              </Paragraph>
            )}

            <Space style={{ marginTop: 16 }}>
              {!showCertForm && !status.certificates_locked && (
                <Button onClick={() => setEditingCerts(true)}>{t('cloudBridge.replaceCertificatesButton')}</Button>
              )}
              <Popconfirm
                title={t('cloudBridge.disconnectConfirmTitle')}
                description={t('cloudBridge.disconnectConfirmDesc')}
                okText={t('common.yes')}
                cancelText={t('common.no')}
                onConfirm={handleDisconnect}
              >
                <Button danger icon={<DisconnectOutlined />} loading={disconnecting}>
                  {t('cloudBridge.disconnectButton')}
                </Button>
              </Popconfirm>
            </Space>

            {showCertForm && (
              <div style={{ marginTop: 24 }}>
                <Paragraph type="secondary">
                  {status.certificates_configured
                    ? t('cloudBridge.replaceCertificatesIntro')
                    : t('cloudBridge.certificatesIntro')}
                </Paragraph>
                <Form form={certForm} layout="vertical" onFinish={handleSaveCertificates}>
                  <PemField form={certForm} name="ca_cert" label={t('cloudBridge.caCertLabel')} />
                  <PemField form={certForm} name="client_cert" label={t('cloudBridge.clientCertLabel')} />
                  <PemField form={certForm} name="client_key" label={t('cloudBridge.clientKeyLabel')} />
                  <Form.Item>
                    <Button type="primary" htmlType="submit" loading={savingCerts}>
                      {t('cloudBridge.saveCertificatesButton')}
                    </Button>
                    {status.certificates_configured && (
                      <Button
                        style={{ marginLeft: 8 }}
                        onClick={() => {
                          certForm.resetFields()
                          setEditingCerts(false)
                        }}
                      >
                        {t('common.cancel')}
                      </Button>
                    )}
                  </Form.Item>
                </Form>
              </div>
            )}
          </>
        )}
      </Card>
    </div>
  )
}
