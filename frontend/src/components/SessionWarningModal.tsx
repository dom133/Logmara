import { Modal, Button, Space, Alert, Progress } from 'antd'
import { useTranslation } from 'react-i18next'
import { WarningOutlined, ReloadOutlined, LogoutOutlined } from '@ant-design/icons'

interface SessionWarningModalProps {
  onExtend: () => Promise<void>
  onLogout: () => void
  countdown: number
}

const TOTAL_SECONDS = 30

export function SessionWarningModal({ onExtend, onLogout, countdown }: SessionWarningModalProps) {
  const { t } = useTranslation()
  const percentage = Math.max(0, Math.min(100, (countdown / TOTAL_SECONDS) * 100))
  return (
    <Modal
      title={<span><WarningOutlined style={{ marginRight: 8 }} />{t('sessionWarning.title')}</span>}
      open={true}
      footer={null}
      closable={false}
      maskClosable={false}
      width={420}
    >
      <Alert
        message={t('sessionWarning.message')}
        description={t('sessionWarning.description', { seconds: countdown })}
        type="warning"
        showIcon
        style={{ marginBottom: 16 }}
      />
      <Progress
        percent={percentage}
        strokeColor={percentage > 50 ? '#faad14' : percentage > 20 ? '#f5222d' : '#ff4d4f'}
        trailColor='#f5f5f5'
        format={() => ''}
        style={{ marginBottom: 16 }}
      />
      <Space style={{ justifyContent: 'flex-end' }}>
        <Button icon={<LogoutOutlined />} onClick={onLogout}>
          {t('sessionWarning.logOut')}
        </Button>
        <Button type="primary" icon={<ReloadOutlined />} onClick={onExtend}>
          {t('sessionWarning.extendSession')}
        </Button>
      </Space>
    </Modal>
  )
}
