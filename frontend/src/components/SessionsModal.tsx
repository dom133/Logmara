import { useEffect, useState } from 'react'
import { Modal, List, Tag, Button, Popconfirm, message, Empty, Spin } from 'antd'
import { getSessions, revokeSession, Session } from '../services/api'
import { getErrorMessage } from '../utils/error'

export function SessionsModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [sessions, setSessions] = useState<Session[]>([])
  const [loading, setLoading] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      setSessions(await getSessions())
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to load sessions'))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (open) load()
  }, [open])

  const handleRevoke = async (id: number) => {
    try {
      await revokeSession(id)
      message.success('Session signed out')
      load()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to revoke session'))
    }
  }

  return (
    <Modal
      title="My Sessions"
      open={open}
      onCancel={onClose}
      footer={[
        <Button key="close" onClick={onClose} style={{ width: '100%' }}>Close</Button>
      ]}
      width={{ sm: '90%', md: 640 }}
    >
      {loading ? (
        <div style={{ textAlign: 'center', padding: 24 }}><Spin /></div>
      ) : sessions.length === 0 ? (
        <Empty description="No active sessions" />
      ) : (
        <List
          dataSource={sessions}
          renderItem={(s) => (
            <List.Item
              actions={[
                s.remember && !s.is_current && (
                  <Popconfirm key="revoke" title="Sign out this device?" onConfirm={() => handleRevoke(s.id)}>
                    <Button size="small" danger>Sign out</Button>
                  </Popconfirm>
                ),
              ].filter(Boolean)}
            >
              <List.Item.Meta
                title={
                  <span>
                    {s.ip || 'Unknown location'}
                    {s.is_current && <Tag color="green" style={{ marginLeft: 8 }}>This device</Tag>}
                    {s.remember && <Tag color="blue" style={{ marginLeft: 8 }}>Remembered</Tag>}
                  </span>
                }
                description={
                  <>
                    <div style={{ wordBreak: 'break-all' }}>{s.user_agent || 'Unknown browser'}</div>
                    <div>
                      Last active: {s.last_used_at ? new Date(s.last_used_at).toLocaleString() : new Date(s.created_at).toLocaleString()}
                    </div>
                  </>
                }
              />
            </List.Item>
          )}
        />
      )}
    </Modal>
  )
}
