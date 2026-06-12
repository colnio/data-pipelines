import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { ModalsProvider } from '@mantine/modals'
import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'
import { AuthProvider, useAuth } from '@/auth/AuthContext'
import { setIsAuthenticated, setIsPrivileged } from '@/router'
import { router } from '@/router'
import { getToken } from '@/api/client'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 60_000,
      refetchOnWindowFocus: false,
      retry: 1,
    },
  },
})

function AppBridge() {
  const { status, user } = useAuth()

  // The route guard reads the LIVE token rather than this render's captured
  // `status`. login() writes the token before navigating to '/', so the guard
  // sees it immediately — avoiding a race where navigate() runs before React
  // re-renders this closure and the guard bounces back to /login. The
  // status==='loading' gate below still blocks rendering until /v1/auth/me
  // validates a stored token on first load.
  setIsAuthenticated(() => status === 'authenticated' || !!getToken())
  setIsPrivileged(() => user?.global_role === 'admin' || user?.global_role === 'pi')

  if (status === 'loading') {
    return (
      <div
        style={{
          display: 'grid',
          placeItems: 'center',
          height: '100vh',
          fontFamily: 'system-ui',
          fontSize: 14,
          color: '#888',
        }}
      >
        Loading…
      </div>
    )
  }

  return <RouterProvider router={router} />
}

const root = document.getElementById('root')
if (!root) throw new Error('No root element')

createRoot(root).render(
  <StrictMode>
    <MantineProvider>
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <AuthProvider>
            <AppBridge />
          </AuthProvider>
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>
  </StrictMode>,
)
