import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
// Required for MessagePlugin / DialogPlugin under React 19.
import 'tdesign-react/es/_util/react-19-adapter'
import { ConfigProvider } from 'tdesign-react'
import zhCN from 'tdesign-react/es/locale/zh_CN'
import 'tdesign-react/es/style/index.css'
import App from './App'
import './index.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ConfigProvider globalConfig={zhCN}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </ConfigProvider>
  </StrictMode>,
)
