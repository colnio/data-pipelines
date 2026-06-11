import { AppShell as MantineAppShell, NavLink, Group, Text, Button, Box } from '@mantine/core'
import { Outlet, Link, useRouterState } from '@tanstack/react-router'
import { useAuth } from '@/auth/AuthContext'

export function AppShell() {
  const { user, logout } = useAuth()
  const routerState = useRouterState()
  const pathname = routerState.location.pathname

  return (
    <MantineAppShell
      header={{ height: 52 }}
      padding="md"
    >
      <MantineAppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group gap="xs">
            <Text fw={700} fz="sm" c="dark">
              Lab Data
            </Text>
            <NavLink
              component={Link}
              to="/"
              label="Runs"
              active={pathname === '/'}
              style={{ borderRadius: 6, padding: '4px 10px' }}
            />
            <NavLink
              component={Link}
              to="/reviews"
              label="Reviews"
              active={pathname.startsWith('/reviews')}
              style={{ borderRadius: 6, padding: '4px 10px' }}
            />
          </Group>
          <Group gap="xs">
            {user && (
              <Text fz="xs" c="dimmed">
                {user.email}
              </Text>
            )}
            <Button size="xs" variant="subtle" color="gray" onClick={logout}>
              Logout
            </Button>
          </Group>
        </Group>
      </MantineAppShell.Header>

      <MantineAppShell.Main>
        <Box maw={1100} mx="auto">
          <Outlet />
        </Box>
      </MantineAppShell.Main>
    </MantineAppShell>
  )
}
