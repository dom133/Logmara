import { useEffect, useState, useCallback } from 'react'

interface SwipeResult {
  swipedLeft: boolean
  swipedRight: boolean
  swipedDown: boolean
  swipedUp: boolean
  onSwipeLeft?: () => void
  onSwipeRight?: () => void
  onSwipeDown?: () => void
  onSwipeUp?: () => void
}

export function useSwipe(
  options: {
    onLeft?: () => void
    onRight?: () => void
    onDown?: () => void
    onUp?: () => void
    threshold?: number
    edgeOnly?: boolean
  } = {},
) {
  const { onLeft, onRight, onDown, onUp, threshold = 50, edgeOnly = false } = options
  const [startPos, setStartPos] = useState<{ x: number; y: number } | null>(null)

  const handleTouchStart = useCallback((e: TouchEvent) => {
    const touch = e.touches[0]
    if (!touch) return
    setStartPos({ x: touch.clientX, y: touch.clientY })
  }, [])

  const handleTouchEnd = useCallback((e: TouchEvent) => {
    if (!startPos) return
    const touch = e.changedTouches[0]
    if (!touch) return

    const dx = touch.clientX - startPos.x
    const dy = touch.clientY - startPos.y
    const absDx = Math.abs(dx)
    const absDy = Math.abs(dy)

    // Check if horizontal swipe is dominant
    if (absDx > absDy && absDx > threshold) {
      if (edgeOnly && touch.clientX > window.innerWidth * 0.3) return
      if (dx < 0 && onLeft) onLeft()
      else if (dx > 0 && onRight) onRight()
    }
    // Check if vertical swipe is dominant
    else if (absDy > absDx && absDy > threshold) {
      if (dy < 0 && onUp) onUp()
      else if (dy > 0 && onDown) onDown()
    }

    setStartPos(null)
  }, [startPos, onLeft, onRight, onDown, onUp, threshold, edgeOnly])

  useEffect(() => {
    document.addEventListener('touchstart', handleTouchStart, { passive: true })
    document.addEventListener('touchend', handleTouchEnd, { passive: true })
    return () => {
      document.removeEventListener('touchstart', handleTouchStart)
      document.removeEventListener('touchend', handleTouchEnd)
    }
  }, [handleTouchStart, handleTouchEnd])

  return { startPos }
}
