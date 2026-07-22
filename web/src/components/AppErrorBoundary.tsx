import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Button } from './ui/button'

type Props = { children: ReactNode }
type State = { error: Error | null }

export default class AppErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Panel UI crashed', error, info.componentStack)
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <main className="min-h-screen bg-zinc-950 text-zinc-100 flex items-center justify-center p-6">
        <div className="max-w-md w-full bg-zinc-900 border border-zinc-800 rounded-lg p-6 shadow-xl space-y-6 text-center">
          <div className="flex justify-center">
            <span className="w-12 h-12 rounded-lg bg-zinc-800 text-zinc-100 font-bold flex items-center justify-center text-lg shadow-inner">
              LA
            </span>
          </div>
          <div className="space-y-2">
            <h1 className="text-xl font-bold tracking-tight">页面加载失败</h1>
            <p className="text-sm text-zinc-400 leading-relaxed">
              {this.state.error.message || '前端发生了未知错误'}
            </p>
          </div>
          <Button
            type="button"
            onClick={() => window.location.reload()}
            className="w-full"
          >
            重新加载页面
          </Button>
        </div>
      </main>
    )
  }
}
