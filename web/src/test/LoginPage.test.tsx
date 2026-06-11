import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { AuthProvider, useAuth } from '@/auth/AuthContext'

function Wrapper({ children }: { children: React.ReactNode }) {
  return (
    <MantineProvider>
      <AuthProvider>{children}</AuthProvider>
    </MantineProvider>
  )
}

const mockToken = 'jwt.token.here'
const mockUser = {
  id: 'user-1',
  email: 'reviewer@example.com',
  display_name: 'Reviewer',
  global_role: 'member',
}

describe('AuthContext login', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', {
      getItem: vi.fn(() => null),
      setItem: vi.fn(),
      removeItem: vi.fn(),
    })
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({
          access_token: mockToken,
          token_type: 'Bearer',
          expires_in: 3600,
          user: mockUser,
        }),
      }),
    )
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('resolves to unauthenticated when no token stored', async () => {
    function StatusDisplay() {
      const { status } = useAuth()
      return <div data-testid="auth-status">{status}</div>
    }

    render(<StatusDisplay />, { wrapper: Wrapper })
    // Should eventually settle to unauthenticated (no stored token)
    await waitFor(() => {
      expect(screen.getByTestId('auth-status').textContent).toBe('unauthenticated')
    })
  })

  it('calls the login API and transitions to authenticated', async () => {
    function LoginTrigger() {
      const { status, login } = useAuth()
      return (
        <div>
          <div data-testid="auth-status">{status}</div>
          <button
            data-testid="login-btn"
            onClick={() => void login('reviewer@example.com', 'password123')}
          >
            Login
          </button>
        </div>
      )
    }

    const user = userEvent.setup()
    render(<LoginTrigger />, { wrapper: Wrapper })

    // Wait for initial loading to resolve
    await waitFor(() => {
      expect(screen.getByTestId('auth-status').textContent).toBe('unauthenticated')
    })

    await user.click(screen.getByTestId('login-btn'))

    await waitFor(() => {
      expect(screen.getByTestId('auth-status').textContent).toBe('authenticated')
    })

    // Token should have been stored
    expect(localStorage.setItem).toHaveBeenCalledWith('lab_data_token', mockToken)
  })
})
