import { Modal, Space, Descriptions, List, Typography, Empty, Button, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { CheckCircleOutlined } from '@ant-design/icons'
import { HistoryGroup } from './historyTypes'
import SeverityTag from './SeverityTag'
import { historyStatusColor } from '../constants/alertConstants'

const { Text } = Typography

interface NotificationDetailModalProps {
  viewing: HistoryGroup | null
  onClose: () => void
}

export default function NotificationDetailModal({ viewing, onClose }: NotificationDetailModalProps) {
  const { t } = useTranslation()
  if (!viewing) return null

  return (
    <Modal
      title={t('notifDetail.title')}
      open={true}
      onCancel={onClose}
      footer={<Button onClick={onClose}>{t('common.close')}</Button>}
      width={{ sm: '90%', md: 640 }}
    >
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <Descriptions column={1} bordered size="small">
          <Descriptions.Item label={t('common.time')}>{new Date(viewing.createdAt).toLocaleString()}</Descriptions.Item>
          <Descriptions.Item label={t('alerts.alert')}>{viewing.alertName || '—'}</Descriptions.Item>
        </Descriptions>

        <div>
          <Text strong>{t('notifDetail.deliveryByChannel')}</Text>
          <div style={{ marginTop: 8 }}>
            <List
              size="small"
              bordered
              dataSource={viewing.channels}
              renderItem={(c) => (
                <List.Item>
                  <Space direction="vertical" size={2} style={{ width: '100%' }}>
                    <Space>
                      <Text strong>{c.channel_name}</Text>
                      <Text type="secondary">({c.channel_type})</Text>
                      <Tag color={historyStatusColor[c.status] || 'orange'}>{c.status}</Tag>
                    </Space>
                    {c.detail && (
                      <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', marginBottom: 0 }} copyable>
                        {c.detail}
                      </Typography.Paragraph>
                    )}
                  </Space>
                </List.Item>
              )}
            />
          </div>
        </div>

        <div>
          <Text strong>{t('notifDetail.triggeringLog')}</Text>
          <div style={{ marginTop: 8 }}>
            {viewing.triggerLog ? (
              <Descriptions column={1} bordered size="small">
                <Descriptions.Item label={t('common.time')}>{new Date(viewing.triggerLog.timestamp).toLocaleString()}</Descriptions.Item>
                <Descriptions.Item label={t('logs.severity')}><SeverityTag severity={viewing.triggerLog.severity} /></Descriptions.Item>
                <Descriptions.Item label={t('logs.host')}>{viewing.triggerLog.hostname} ({viewing.triggerLog.fromhost_ip})</Descriptions.Item>
                {viewing.triggerLog.app_name && (
                  <Descriptions.Item label={t('dashboard.app')}>{viewing.triggerLog.app_name}</Descriptions.Item>
                )}
                <Descriptions.Item label={t('logs.message')}>
                  <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', marginBottom: 0 }} copyable>
                    {viewing.triggerLog.message}
                  </Typography.Paragraph>
                </Descriptions.Item>
              </Descriptions>
            ) : (
              <Empty description={t('notifDetail.noTriggerLog')} image={Empty.PRESENTED_IMAGE_SIMPLE} />
            )}
          </div>
        </div>

        {viewing.auditLogRef && (
          <div>
            <Text strong>{t('notifDetail.auditLogContext')}</Text>
            <div style={{ marginTop: 8 }}>
              <Descriptions column={1} bordered size="small">
                <Descriptions.Item label={t('common.time')}>{new Date(viewing.auditLogRef.timestamp).toLocaleString()}</Descriptions.Item>
                <Descriptions.Item label={t('notifDetail.action')}>{viewing.auditLogRef.action}</Descriptions.Item>
                <Descriptions.Item label={t('notifDetail.user')}>{viewing.auditLogRef.username}</Descriptions.Item>
                <Descriptions.Item label={t('notifDetail.ip')}>{viewing.auditLogRef.user_ip}</Descriptions.Item>
                {viewing.auditLogRef.details && (
                  <Descriptions.Item label={t('common.details')}>
                    <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', marginBottom: 0 }} copyable>
                      {viewing.auditLogRef.details}
                    </Typography.Paragraph>
                  </Descriptions.Item>
                )}
              </Descriptions>
            </div>
          </div>
        )}

        <div>
          <Text strong>{t('notifDetail.conditionsMet')}</Text>
          <div style={{ marginTop: 8 }}>
            {viewing.matchedConditions && viewing.matchedConditions.length > 0 ? (
              <List
                size="small"
                bordered
                dataSource={viewing.matchedConditions}
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
              <Empty description={t('notifDetail.noConditions')} image={Empty.PRESENTED_IMAGE_SIMPLE} />
            )}
          </div>
        </div>
      </Space>
    </Modal>
  )
}
