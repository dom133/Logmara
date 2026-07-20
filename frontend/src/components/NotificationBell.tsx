import { useEffect, useRef, useState } from 'react'
import { Badge, Button, Card, Drawer, Dropdown, Empty, List, Tag, Typography } from 'antd'
import { BellOutlined } from '@ant-design/icons'
import { getNotifications, markNotificationsRead, streamNotifications, InAppNotification } from '../services/api'
import { useIsMobile } from '../hooks/useIsMobile'

const severityColor: Record<string, string> = {
  critical: 'red',
  error: 'volcano',
  warning: 'orange',
  info: 'blue',
}

function formatTime(iso: string) {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  return d.toLocaleString()
}

export function NotificationBell() {
  const isMobile = useIsMobile()
  const [enabled, setEnabled] = useState(false)
  const [items, setItems] = useState<InAppNotification[]>([])
  const [unreadCount, setUnreadCount] = useState(0)
  const [open, setOpen] = useState(false)
  const lastIdRef = useRef(0)

  useEffect(() => {
    let cancelled = false
    let stopStream: (() => void) | null = null

    getNotifications().then(res => {
      if (cancelled) return
      setEnabled(res.enabled !== false)
      setItems(res.notifications || [])
      setUnreadCount(res.unread_count || 0)
      lastIdRef.current = res.last_id || 0

      if (res.enabled !== false) {
        stopStream = streamNotifications((n) => {
          setItems(prev => [n, ...prev].slice(0, 50))
          setUnreadCount(prev => prev + 1)
        })
      }
    }).catch(() => { /* ignore */ })

    return () => {
      cancelled = true
      stopStream?.()
    }
  }, [])

  if (!enabled) return null

  const handleOpen = () => {
    setOpen(true)
    if (unreadCount > 0) {
      const maxID = items.reduce((max, n) => Math.max(max, n.id), lastIdRef.current)
      lastIdRef.current = maxID
      markNotificationsRead(maxID).catch(() => { /* ignore */ })
      setUnreadCount(0)
    }
  }

  const handleClearAll = () => {
    markNotificationsRead(lastIdRef.current).catch(() => { /* ignore */ })
    setItems([])
    setUnreadCount(0)
  }

  const panelBody = items.length === 0 ? (
    <Empty description="No notifications" style={{ padding: 24 }} />
  ) : (
    <List
      dataSource={items}
      renderItem={(n) => (
        <List.Item style={{ padding: '10px 16px', display: 'block' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
            <Typography.Text strong style={{ fontSize: 13 }}>{n.title}</Typography.Text>
            <Tag color={severityColor[n.severity] || 'default'} style={{ marginRight: 0 }}>{n.severity}</Tag>
          </div>
          <Typography.Text type="secondary" style={{ fontSize: 13, display: 'block', marginTop: 2 }}>{n.message}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 11, display: 'block', marginTop: 4 }}>{formatTime(n.created_at)}</Typography.Text>
        </List.Item>
      )}
    />
  )

  const clearButton = items.length > 0 && (
    <Button type="link" size="small" style={{ padding: 0 }} onClick={handleClearAll}>Clear all</Button>
  )

  const bellButton = (
    <Button
      type="text"
      onClick={isMobile ? handleOpen : undefined}
      icon={
        <Badge count={unreadCount} size="small" offset={[-2, 2]}>
          <BellOutlined style={{ fontSize: 18 }} />
        </Badge>
      }
    />
  )

  if (isMobile) {
    return (
      <>
        {bellButton}
        <Drawer
          title="Notifications"
          placement="bottom"
          height="70%"
          open={open}
          onClose={() => setOpen(false)}
          extra={clearButton}
          styles={{ body: { padding: 0, overflowY: 'auto' } }}
        >
          {panelBody}
        </Drawer>
      </>
    )
  }

  return (
    <Dropdown
      open={open}
      onOpenChange={(next) => { if (next) handleOpen(); else setOpen(false) }}
      trigger={['click']}
      placement="bottomRight"
      dropdownRender={() => (
        <Card
          size="small"
          title="Notifications"
          extra={clearButton}
          style={{ width: 360 }}
          styles={{ body: { padding: 0, maxHeight: 380, overflowY: 'auto' } }}
        >
          {panelBody}
        </Card>
      )}
    >
      {bellButton}
    </Dropdown>
  )
}
