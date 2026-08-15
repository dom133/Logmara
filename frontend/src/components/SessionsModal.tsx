import { useEffect, useState } from 'react'
import { Modal, List, Tag, Button, Popconfirm, message, Empty, Spin } from 'antd'
import { useTranslation } from 'react-i18next'
import { getSessions, revokeSession, Session } from '../services/api'
import { getErrorMessage } from '../utils/error'

export function SessionsModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const [sessions, setSessions] = useState<Session[]>([])
  const [loading, setLoading] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      setSessions(await getSessions())
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('sessions.loadFailed')))
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
      message.success(t('sessions.signedOut'))
      load()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('sessions.revokeFailed')))
    }
  }

  return (
    <Modal
      title={t('nav.sessions')}
      open={open}
      onCancel={onClose}
      footer={[
        <Button key="close" onClick={onClose} style={{ width: '100%' }}>{t('common.close')}</Button>
      ]}
      width={{ sm: '90%', md: 640 }}
    >
      {loading ? (
        <div style={{ textAlign: 'center', padding: 24 }}><Spin /></div>
      ) : sessions.length === 0 ? (
        <Empty description={t('sessions.noActive')} />
      ) : (
        <List
          dataSource={sessions}
          renderItem={(s) => (
            <List.Item
              actions={[
                s.remember && !s.is_current && (
                  <Popconfirm key="revoke" title={t('sessions.signOutConfirm')} onConfirm={() => handleRevoke(s.id)}>
                    <Button size="small" danger>{t('sessions.signOut')}</Button>
                  </Popconfirm>
                ),
              ].filter(Boolean)}
            >
              <List.Item.Meta
                 title={
                   <span>
                     {s.ip || t('sessions.unknownLocation')}
                     {s.is_current && <Tag color="green" style={{ marginLeft: 8 }}>{t('sessions.thisDevice')}</Tag>}
                     {s.remember && <Tag color="blue" style={{ marginLeft: 8 }}>{t('sessions.remembered')}</Tag>}
                   </span>
                 }
                 description={
                   <>
                     <div style={{ wordBreak: 'break-all' }}>{s.user_agent || t('sessions.unknownBrowser')}</div>
                     {s.screen_resolution && <div>{t('sessions.screenResolution')}: {s.screen_resolution}</div>}
                     {s.timezone && <div>{t('sessions.timezone')}: {s.timezone}</div>}
                     <div>
                       {t('sessions.lastActive', { date: s.last_used_at ? new Date(s.last_used_at).toLocaleString() : new Date(s.created_at).toLocaleString() })}
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
