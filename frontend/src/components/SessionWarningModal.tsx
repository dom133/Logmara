import { Modal, Button, Space, Alert } from 'antd'
import { WarningOutlined, ReloadOutlined, LogoutOutlined } from '@ant-design/icons'

interface SessionWarningModalProps {
  onExtend: () => Promise<void>
  onLogout: () => void
  countdown: number
}

export function SessionWarningModal({ onExtend, onLogout, countdown }: SessionWarningModalProps) {
  return (
    <Modal
      title={<span><WarningOutlined style={{ marginRight: 8 }} />Session Expiring</span>}
      open={true}
      footer={null}
      closable={false}
      maskClosable={false}
      width={420}
    >
      <Alert
        message="Your session is about to expire"
        description={`Session will expire in ${countdown} seconds. Extend it or log out now.`
        }
        type="warning"
        showIcon
        style={{ marginBottom: 16 }}
      />
      <Space style={{ justifyContent: 'flex-end' }}>
        <Button icon={<LogoutOutlined />} onClick={onLogout}>
          Log Out
        </Button>
        <Button type="primary" icon={<ReloadOutlined />} onClick={onExtend}>
          Extend Session
        </Button>
      </Space>
    </Modal>
  )
}