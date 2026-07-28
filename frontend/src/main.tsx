import React from 'react'
import ReactDOM from 'react-dom/client'
import { unstableSetRender } from 'antd'
import App from './App'

// antd's static functions (message.xxx, notification.xxx, Modal.confirm) mount
// themselves outside the app's own React tree via rc-util's render() helper.
// That helper feature-detects createRoot by checking react-dom's *main* entry
// (`import * as ReactDOM from 'react-dom'`) - but React 19 moved createRoot to
// react-dom/client and dropped the legacy `render`/`unmountComponentAtNode`
// exports from the main entry entirely, so the detection finds neither and
// silently no-ops (no error, nothing mounted - see https://u.ant.design/v5-for-19).
// Overriding it with the real react-dom/client APIs is antd's own documented
// workaround for React 19 until rc-util fixes the detection upstream.
const antdRootMark = Symbol('antdRoot')
unstableSetRender((node, container) => {
  const target = container as Element & { [antdRootMark]?: ReactDOM.Root }
  const root = target[antdRootMark] ?? ReactDOM.createRoot(target)
  target[antdRootMark] = root
  root.render(node)
  return async () => {
    await Promise.resolve()
    root.unmount()
    delete target[antdRootMark]
  }
})

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch((err) => {
      console.warn('Service worker registration failed', err)
    })
  })
}