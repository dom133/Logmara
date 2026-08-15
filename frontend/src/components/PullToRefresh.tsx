import { useState, useCallback, useRef, useEffect } from 'react'
import { tokens } from '../theme/tokens'

const PULL_THRESHOLD = 60
const PULL_COMMIT = 100

export default function PullToRefresh({ onRefresh, children }: {
  onRefresh: () => Promise<void>
  children: React.ReactNode
}) {
  const [pulling, setPulling] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [pullDistance, setPullDistance] = useState(0)
  const startY = useRef(0)
  const startRef = useRef(0)

  const handleTouchStart = useCallback((e: TouchEvent) => {
    if (e.targetTouches.length !== 1) return
    const scrollTop = (e.target as HTMLElement)?.scrollTop ?? 0
    if (scrollTop > 0) return
    startY.current = e.touches[0].clientY
    startRef.current = Date.now()
    setPulling(true)
  }, [])

  const handleTouchMove = useCallback((e: TouchEvent) => {
    if (!pulling || refreshing) return
    const dy = e.touches[0].clientY - startY.current
    if (dy < 0) return
    setPullDistance(Math.min(dy, 120))
  }, [pulling, refreshing])

  const handleTouchEnd = useCallback(async () => {
    if (!pulling) return
    if (pullDistance > PULL_COMMIT) {
      setRefreshing(true)
      try {
        await onRefresh()
      } finally {
        setRefreshing(false)
      }
    }
    setPulling(false)
    setPullDistance(0)
  }, [pulling, pullDistance, onRefresh])

  useEffect(() => {
    const el = document.querySelector('.ant-layout-content') as HTMLElement | null
    if (!el) return
    el.addEventListener('touchstart', handleTouchStart as any, { passive: true })
    el.addEventListener('touchmove', handleTouchMove as any, { passive: false })
    el.addEventListener('touchend', handleTouchEnd as any, { passive: true })
    return () => {
      el.removeEventListener('touchstart', handleTouchStart as any)
      el.removeEventListener('touchmove', handleTouchMove as any)
      el.removeEventListener('touchend', handleTouchEnd as any)
    }
  }, [handleTouchStart, handleTouchMove, handleTouchEnd])

  const progress = Math.min(pullDistance / PULL_THRESHOLD, 1)
  const iconRotation = progress * 180

  return (
    <>
      <div
        style={{
          height: pulling || refreshing ? 40 + pullDistance * 0.3 : 0,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          overflow: 'hidden',
          transition: refreshing ? 'none' : 'height 0.2s ease',
        }}
      >
        {refreshing ? (
          <div className="animate-pulse" style={{ color: tokens.colors.primary, fontSize: 18 }}>
            &#x21BB; Refreshing...
          </div>
        ) : (
          <div
            style={{
              color: tokens.colors.primary,
              fontSize: 18,
              transform: `rotate(${iconRotation}deg)`,
              transition: 'transform 0.15s ease',
              opacity: progress,
            }}
          >
            &#x2193;
            {progress >= 1 && <span style={{ marginLeft: 8 }}>Release to refresh</span>}
          </div>
        )}
      </div>
      {children}
    </>
  )
}
