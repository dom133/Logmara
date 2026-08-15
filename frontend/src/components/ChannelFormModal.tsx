import { Form, Input, InputNumber, Select, Switch, Space, Modal, Typography } from 'antd'
import { NotificationChannel, NotificationChannelRequest, NotificationChannelType, UserSummary } from '../services/api'
import { channelTypeLabels } from '../constants/alertConstants'

const { Text } = Typography

interface ChannelFormModalProps {
  open: boolean
  editing: NotificationChannel | null
  users: UserSummary[]
  form: ReturnType<typeof Form.useForm>[0]
  onCancel: () => void
  onOk: () => void
}

export default function ChannelFormModal({ open, editing, users, form, onCancel, onOk }: ChannelFormModalProps) {
  const channelType = Form.useWatch('type', form)

  return (
    <Modal
      title={editing ? 'Edit Notification Channel' : 'New Notification Channel'}
      open={open}
      onCancel={onCancel}
      onOk={onOk}
      width={{ sm: '90%', md: 560 }}
    >
      <Form form={form} layout="vertical">
        <Form.Item name="name" label="Name" rules={[{ required: true }]}>
          <Input />
        </Form.Item>
        <Form.Item name="type" label="Type" rules={[{ required: true }]}>
          <Select
            options={Object.entries(channelTypeLabels).map(([value, label]) => ({ value, label }))}
            disabled={!!editing}
          />
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
          <>
            <Form.Item name="user_ids" label="Target Users" tooltip="Leave empty to broadcast to all users">
              <Select mode="multiple" placeholder="Select users or leave empty for all" options={users.map(u => ({ value: u.id, label: u.username }))} />
            </Form.Item>
            <Text type="secondary">Delivers to the notification bell for selected users (or all if empty).</Text>
          </>
        )}
        {channelType === 'push' && (
          <>
            <Form.Item name="user_ids" label="Target Users" tooltip="Leave empty to broadcast to all users">
              <Select mode="multiple" placeholder="Select users or leave empty for all" options={users.map(u => ({ value: u.id, label: u.username }))} />
            </Form.Item>
            <Text type="secondary">Delivers a browser push notification to selected users (or all if empty).</Text>
          </>
        )}

        <Form.Item name="enabled" label="Enabled" valuePropName="checked">
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  )
}
