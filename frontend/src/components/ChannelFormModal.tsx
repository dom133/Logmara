import { Form, Input, InputNumber, Select, Switch, Space, Modal, Typography } from 'antd'
import { useTranslation } from 'react-i18next'
import { NotificationChannel, NotificationChannelRequest, NotificationChannelType, UserSummary } from '../services/api'
import { getChannelTypeLabels } from '../constants/alertConstants'

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
  const { t } = useTranslation()
  const channelType = Form.useWatch('type', form)

  return (
    <Modal
      title={editing ? t('alerts.editChannel') : t('alerts.newChannel')}
      open={open}
      onCancel={onCancel}
      onOk={onOk}
      width={{ sm: '90%', md: 560 }}
    >
      <Form form={form} layout="vertical">
        <Form.Item name="name" label={t('common.name')} rules={[{ required: true }]}>
          <Input />
        </Form.Item>
        <Form.Item name="type" label={t('common.type')} rules={[{ required: true }]}>
          <Select
            options={Object.entries(getChannelTypeLabels(t)).map(([value, label]) => ({ value, label }))}
            disabled={!!editing}
          />
        </Form.Item>

        {channelType === 'email' && (
          <Form.Item name="to" label={t('channelForm.recipients')} rules={[{ required: true }]} tooltip={t('channelForm.recipientsTooltip')}>
            <Select mode="tags" open={false} placeholder={t('channelForm.recipientsPlaceholder')} tokenSeparators={[',', ' ']} />
          </Form.Item>
        )}
        {channelType === 'webhook' && (
          <>
            <Form.Item name="url" label={t('alerts.webhookUrl')} rules={[{ required: true }]}>
              <Input placeholder={t('channelForm.webhookUrlPlaceholder')} />
            </Form.Item>
            <Form.Item name="secret" label={t('channelForm.bearerToken')} tooltip={t('channelForm.bearerTokenTooltip')}>
              <Input.Password />
            </Form.Item>
          </>
        )}
        {(channelType === 'slack' || channelType === 'teams') && (
          <Form.Item name="webhook_url" label={t('channelForm.incomingWebhookUrl')} rules={[{ required: true }]}>
            <Input placeholder={t('channelForm.slackUrlPlaceholder')} />
          </Form.Item>
        )}
        {channelType === 'in_app' && (
          <>
            <Form.Item name="user_ids" label={t('channelForm.targetUsers')} tooltip={t('channelForm.targetUsersTooltip')}>
              <Select mode="multiple" placeholder={t('channelForm.selectUsersPlaceholder')} options={users.map(u => ({ value: u.id, label: u.username }))} />
            </Form.Item>
            <Text type="secondary">{t('channelForm.inAppDeliveryNote')}</Text>
          </>
        )}
        {channelType === 'push' && (
          <>
            <Form.Item name="user_ids" label={t('channelForm.targetUsers')} tooltip={t('channelForm.targetUsersTooltip')}>
              <Select mode="multiple" placeholder={t('channelForm.selectUsersPlaceholder')} options={users.map(u => ({ value: u.id, label: u.username }))} />
            </Form.Item>
            <Text type="secondary">{t('channelForm.pushDeliveryNote')}</Text>
          </>
        )}

        <Form.Item name="enabled" label={t('common.enabled')} valuePropName="checked">
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  )
}
