import { Modal, Button, Space, Alert } from 'antd'
import { useTranslation } from 'react-i18next'
import { WarningOutlined, ReloadOutlined, LogoutOutlined } from '@ant-design/icons'

interface SessionWarningModalProps {
  onExtend: () => Promise<void>
  onLogout: () => void
  countdown: number
}

export function SessionWarningModal({ onExtend, onLogout, countdown }: SessionWarningModalProps) {
  const { t } = useTranslation()
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
