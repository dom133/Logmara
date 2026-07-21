import { useEffect, useRef, useState } from 'react'
import { Badge, Button, Card, Drawer, Dropdown, Empty, List, Space, Switch, Tag, Typography, message } from 'antd'
import { BellOutlined } from '@ant-design/icons'
import {
  getNotifications, markNotificationsRead, streamNotifications, InAppNotification,
  getVapidPublicKey, subscribePush, unsubscribePush,
} from '../services/api'
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

const pushSupported = typeof window !== 'undefined' && 'serviceWorker' in navigator && 'PushManager' in window

function urlBase64ToUint8Array(base64String: string) {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4)
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/')
  const rawData = window.atob(base64)
  const outputArray = new Uint8Array(rawData.length)
  for (let i = 0; i < rawData.length; ++i) outputArray[i] = rawData.charCodeAt(i)
  return outputArray
}

export function NotificationBell() {
  const isMobile = useIsMobile()
  const [enabled, setEnabled] = useState(false)
  const [items, setItems] = useState<InAppNotification[]>([])
  const [unreadCount, setUnreadCount] = useState(0)
  const [open, setOpen] = useState(false)
  const [pushSubscribed, setPushSubscribed] = useState(false)
  const [pushBusy, setPushBusy] = useState(false)
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

  useEffect(() => {
    if (!enabled || !pushSupported) return
    let cancelled = false
    navigator.serviceWorker.ready
      .then(reg => reg.pushManager.getSubscription())
      .then(sub => { if (!cancelled) setPushSubscribed(!!sub) })
      .catch(() => { /* ignore */ })
    return () => { cancelled = true }
  }, [enabled])

  if (!enabled) return null

  const handleTogglePush = async (checked: boolean) => {
    setPushBusy(true)
    try {
      if (checked) {
        // Request permission first, as the very first await in this
        // handler - it needs to run on the click's user-activation, and
        // waiting on anything else (like serviceWorker.ready) beforehand
        // risks losing that on stricter mobile browsers, or hanging here
        // indefinitely if the service worker is stuck installing.
        const permission = await Notification.requestPermission()
        if (permission !== 'granted') {
          message.warning('Notification permission was not granted')
          return
        }
        const reg = await navigator.serviceWorker.ready
        const publicKey = await getVapidPublicKey()
        const sub = await reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: urlBase64ToUint8Array(publicKey),
        })
        await subscribePush(sub.toJSON())
        setPushSubscribed(true)
        message.success('Push notifications enabled')
      } else {
        const reg = await navigator.serviceWorker.ready
        const sub = await reg.pushManager.getSubscription()
        if (sub) {
          await unsubscribePush(sub.endpoint)
          await sub.unsubscribe()
        }
        setPushSubscribed(false)
        message.success('Push notifications disabled')
      }
    } catch {
      message.error('Failed to update push notification setting')
    } finally {
      setPushBusy(false)
    }
  }

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

  const pushRow = pushSupported && (
    <div style={{ padding: '8px 16px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid rgba(128,128,128,0.2)' }}>
      <Typography.Text style={{ fontSize: 12 }}>Push notifications</Typography.Text>
      <Switch size="small" checked={pushSubscribed} loading={pushBusy} onChange={handleTogglePush} />
    </div>
  )

  const panelContent = (
    <>
      {pushRow}
      {panelBody}
    </>
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
          {panelContent}
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
          {panelContent}
        </Card>
      )}
    >
      {bellButton}
    </Dropdown>
  )
}
