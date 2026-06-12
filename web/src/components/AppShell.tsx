import {
  AppShell as MantineAppShell,
  NavLink,
  Group,
  Text,
  Button,
  Box,
  Burger,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { Outlet, Link, useRouterState } from '@tanstack/react-router'
import { useAuth } from '@/auth/AuthContext'
import { useIsPrivileged } from '@/auth/useRole'

export function AppShell() {
  const { user, logout } = useAuth()
  const privileged = useIsPrivileged()
  const [opened, { toggle, close }] = useDisclosure()
  const routerState = useRouterState()
  const pathname = routerState.location.pathname

  const navItems = [
    { to: '/', label: 'Runs', active: pathname === '/' },
    { to: '/reviews', label: 'Reviews', active: pathname.startsWith('/reviews') },
    { to: '/catalog', label: 'Catalog', active: pathname.startsWith('/catalog') },
    { to: '/jupyter', label: 'Jupyter', active: pathname.startsWith('/jupyter') },
  ]

  return (
    <MantineAppShell
      header={{ height: 52 }}
      navbar={{ width: 200, breakpoint: 'sm', collapsed: { mobile: !opened } }}
      padding="md"
    >
      <MantineAppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group gap="xs">
            <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" />
            <Text fw={700} fz="sm" c="dark">
              Lab Data
            </Text>
          </Group>
          <Group gap="xs">
            {user && (
              <Text fz="xs" c="dimmed">
                {user.email}
                {user.global_role ? ` · ${user.global_role}` : ''}
              </Text>
            )}
            <Button size="xs" variant="subtle" color="gray" onClick={logout}>
              Logout
            </Button>
          </Group>
        </Group>
      </MantineAppShell.Header>

      <MantineAppShell.Navbar p="xs">
        {navItems.map((item) => (
          <NavLink
            key={item.to}
            component={Link}
            to={item.to}
            label={item.label}
            active={item.active}
            onClick={close}
            style={{ borderRadius: 6 }}
          />
        ))}
        {privileged && (
          <NavLink
            component={Link}
            to="/admin"
            label="Admin"
            active={pathname.startsWith('/admin')}
            onClick={close}
            style={{ borderRadius: 6 }}
          />
        )}
      </MantineAppShell.Navbar>

      <MantineAppShell.Main>
        <Box maw={1100} mx="auto">
          <Outlet />
        </Box>
      </MantineAppShell.Main>
    </MantineAppShell>
  )
}
