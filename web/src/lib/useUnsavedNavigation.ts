import { useCallback, useEffect, useRef } from 'react'
import { useBlocker } from 'react-router-dom'
import type { BlockerFunction } from 'react-router'

type UnsavedNavigationOptions = {
  active: boolean
  title?: string
  message?: string
}

export function useUnsavedNavigation({
  active,
  title = '放弃未保存的更改？',
  message = '离开当前页面会丢失尚未保存的修改。',
}: UnsavedNavigationOptions) {
  const allowNextRef = useRef(false)
  const shouldBlock = useCallback<BlockerFunction>(
    ({ currentLocation, nextLocation }) => {
      if (allowNextRef.current) {
        allowNextRef.current = false
        return false
      }
      if (!active || nextLocation.pathname === '/login') return false
      return (
        currentLocation.pathname !== nextLocation.pathname ||
        currentLocation.search !== nextLocation.search ||
        currentLocation.hash !== nextLocation.hash
      )
    },
    [active],
  )
  const blocker = useBlocker(
    shouldBlock,
  )

  const blockerRef = useRef(blocker)
  blockerRef.current = blocker
  const blocked = blocker.state === 'blocked'

  useEffect(() => {
    if (!blocked) return

    const current = blockerRef.current
    const confirmLeave = window.confirm(`${title}\n${message}`)
    if (confirmLeave) {
      current.proceed?.()
    } else {
      current.reset?.()
    }
  }, [blocked, message, title])

  return useCallback(() => {
    allowNextRef.current = true
  }, [])
}
