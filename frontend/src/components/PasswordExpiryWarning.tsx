import { useEffect, useState } from 'react'
import { Alert } from 'antd'
import { useAuth } from '../services/auth'
import { useTranslation } from 'react-i18next'

const WARNING_DAYS = 7

export function PasswordExpiryWarning() {
  const { user } = useAuth()
  const { t } = useTranslation()
  const [daysLeft, setDaysLeft] = useState<number | null>(null)

  useEffect(() => {
    if (!user?.password_expires_at) {
      setDaysLeft(null)
      return
    }
    const msLeft = user.password_expires_at * 1000 - Date.now()
    const days = Math.ceil(msLeft / (1000 * 60 * 60 * 24))
    setDaysLeft(days)
  }, [user?.password_expires_at])

  if (daysLeft === null || daysLeft > WARNING_DAYS || daysLeft < 0) {
    return null
  }

  return (
    <Alert
      message={t('login.passwordExpiringSoon')}
      description={`${t('login.passwordExpiresAt')}: ${new Date(user!.password_expires_at! * 1000).toLocaleDateString()} (${daysLeft} ${daysLeft === 1 ? 'day' : 'days'})`}
      type="warning"
      showIcon
      style={{ marginBottom: 8 }}
    />
  )
}
