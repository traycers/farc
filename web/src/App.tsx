import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import StoragesPage from './pages/StoragesPage'
import ChannelsPage from './pages/ChannelsPage'
import PlayerPage from './pages/PlayerPage'

export default function App() {
  return (
    <>
      <nav>
        <NavLink to="/storages" className={({ isActive }) => (isActive ? 'active' : '')}>
          Storages
        </NavLink>
        <NavLink to="/channels" className={({ isActive }) => (isActive ? 'active' : '')}>
          Channels
        </NavLink>
        <NavLink to="/player" className={({ isActive }) => (isActive ? 'active' : '')}>
          Player
        </NavLink>
      </nav>
      <main>
        <Routes>
          <Route path="/" element={<Navigate to="/storages" replace />} />
          <Route path="/storages" element={<StoragesPage />} />
          <Route path="/channels" element={<ChannelsPage />} />
          <Route path="/player" element={<PlayerPage />} />
        </Routes>
      </main>
    </>
  )
}
