import { Modal, Space, Descriptions, List, Typography, Empty, Button, Tag } from 'antd'
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
  if (!viewing) return null

  return (
    <Modal
      title="Notification Detail"
      open={true}
      onCancel={onClose}
      footer={<Button onClick={onClose}>Close</Button>}
      width={{ sm: '90%', md: 640 }}
    >
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        <Descriptions column={1} bordered size="small">
          <Descriptions.Item label="Time">{new Date(viewing.createdAt).toLocaleString()}</Descriptions.Item>
          <Descriptions.Item label="Alert">{viewing.alertName || '\u2014'}</Descriptions.Item>
        </Descriptions>

        <div>
          <Text strong>Delivery by channel</Text>
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
          <Text strong>Triggering log</Text>
          <div style={{ marginTop: 8 }}>
            {viewing.triggerLog ? (
              <Descriptions column={1} bordered size="small">
                <Descriptions.Item label="Time">{new Date(viewing.triggerLog.timestamp).toLocaleString()}</Descriptions.Item>
                <Descriptions.Item label="Severity"><SeverityTag severity={viewing.triggerLog.severity} /></Descriptions.Item>
                <Descriptions.Item label="Host">{viewing.triggerLog.hostname} ({viewing.triggerLog.fromhost_ip})</Descriptions.Item>
                {viewing.triggerLog.app_name && (
                  <Descriptions.Item label="App">{viewing.triggerLog.app_name}</Descriptions.Item>
                )}
                <Descriptions.Item label="Message">
                  <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', marginBottom: 0 }} copyable>
                    {viewing.triggerLog.message}
                  </Typography.Paragraph>
                </Descriptions.Item>
              </Descriptions>
            ) : (
              <Empty description="No log entry associated with this notification (e.g. an audit-log alert)" image={Empty.PRESENTED_IMAGE_SIMPLE} />
            )}
          </div>
        </div>

        {viewing.auditLogRef && (
          <div>
            <Text strong>Audit log context</Text>
            <div style={{ marginTop: 8 }}>
              <Descriptions column={1} bordered size="small">
                <Descriptions.Item label="Time">{new Date(viewing.auditLogRef.timestamp).toLocaleString()}</Descriptions.Item>
                <Descriptions.Item label="Action">{viewing.auditLogRef.action}</Descriptions.Item>
                <Descriptions.Item label="User">{viewing.auditLogRef.username}</Descriptions.Item>
                <Descriptions.Item label="IP">{viewing.auditLogRef.user_ip}</Descriptions.Item>
                {viewing.auditLogRef.details && (
                  <Descriptions.Item label="Details">
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
          <Text strong>Conditions met</Text>
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
              <Empty description="No condition breakdown recorded for this notification" image={Empty.PRESENTED_IMAGE_SIMPLE} />
            )}
          </div>
        </div>
      </Space>
    </Modal>
  )
}
