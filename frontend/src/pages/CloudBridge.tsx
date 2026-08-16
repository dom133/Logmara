import { useEffect, useState } from 'react'
import { Card, Form, Input, Button, Tag, Typography, message, Descriptions } from 'antd'
import { useTranslation } from 'react-i18next'
import { CloudOutlined, CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons'
import { getCloudBridgeStatus, submitCloudBridgeLink, CloudBridgeStatus } from '../services/api'
import { getErrorMessage } from '../utils/error'

const { Text, Paragraph } = Typography

// Polls while the tab is open so "connected" reflects the tunnel's real
// state (it can flip without any action here - the agent reconnects on
// its own) instead of only updating on the next full page load.
const STATUS_POLL_MS = 5000

export default function CloudBridge() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<CloudBridgeStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [form] = Form.useForm()
  const [submitting, setSubmitting] = useState(false)

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
      await submitCloudBridgeLink(link)
      message.success(t('cloudBridge.pairSuccess'))
      form.resetFields()
      await loadStatus()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('cloudBridge.pairFailed')))
    } finally {
      setSubmitting(false)
    }
  }

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
          <Descriptions column={1} bordered size="small">
            <Descriptions.Item label={t('cloudBridge.instanceId')}>
              <Text code copyable>
                {status.instance_id}
              </Text>
            </Descriptions.Item>
            <Descriptions.Item label={t('cloudBridge.status')}>
              {status.connected ? (
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
        )}
      </Card>
    </div>
  )
}
