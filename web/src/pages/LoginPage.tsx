import { useState } from 'react'
import {
  Box,
  Button,
  Paper,
  PasswordInput,
  Text,
  TextInput,
  Title,
  Alert,
} from '@mantine/core'
import { useNavigate } from '@tanstack/react-router'
import { useAuth } from '@/auth/AuthContext'

export function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    setLoading(true)
    try {
      await login(email, password)
      void navigate({ to: '/' })
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <Box
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: '#f8f9fa',
      }}
    >
      <Paper shadow="sm" p="xl" radius="md" w={360}>
        <Title order={3} mb="xs">
          Lab Data Review
        </Title>
        <Text c="dimmed" fz="sm" mb="lg">
          Sign in with your reviewer account
        </Text>

        {error && (
          <Alert color="red" mb="md" radius="sm">
            {error}
          </Alert>
        )}

        <form onSubmit={handleSubmit}>
          <TextInput
            label="Email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.currentTarget.value)}
            required
            mb="sm"
            data-testid="email-input"
          />
          <PasswordInput
            label="Password"
            value={password}
            onChange={(e) => setPassword(e.currentTarget.value)}
            required
            mb="lg"
            data-testid="password-input"
          />
          <Button type="submit" fullWidth loading={loading} data-testid="login-button">
            Sign in
          </Button>
        </form>
      </Paper>
    </Box>
  )
}
