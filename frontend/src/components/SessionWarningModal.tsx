import { useState, useEffect } from 'react'
import { useAuth } from '../services/auth'

interface SessionWarningModalProps {
  onExtend: () => void
  onLogout: () => void
  countdown: number
}

export function SessionWarningModal({ onExtend, onLogout, countdown }: SessionWarningModalProps) {
  const [timeLeft, setTimeLeft] = useState(countdown)
  
  useEffect(() => {
    if (countdown > 0) {
      const timer = setTimeout(() => {
        setTimeLeft(prev => Math.max(0, prev - 1))
      }, 1000)
      
      return () => clearTimeout(timer)
    }
  }, [countdown])
  
  const handleExtend = () => {
    onExtend()
  }
  
  const handleLogout = () => {
    onLogout()
  }
  
  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg p-6 max-w-md w-full mx-4">
        <h3 className="text-lg font-bold mb-4">Session Expiring Soon</h3>
        <p className="mb-4">
          Your session will expire in {timeLeft} seconds. 
          Do you want to extend your session or log out?
        </p>
        <div className="flex justify-end space-x-3">
          <button
            onClick={handleLogout}
            className="px-4 py-2 bg-red-600 text-white rounded hover:bg-red-700 transition"
          >
            Log Out
          </button>
          <button
            onClick={handleExtend}
            className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 transition"
          >
            Extend Session
          </button>
        </div>
      </div>
    </div>
  )
}