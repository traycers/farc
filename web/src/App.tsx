import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import StoragesPage from './pages/StoragesPage'
import ChannelsPage from './pages/ChannelsPage'
import PlayerPage from './pages/PlayerPage'

function navLinkClass({ isActive }: { isActive: boolean }) {
  return `nav-link${isActive ? ' active' : ''}`
}

export default function App() {
  return (
    <>
      <nav className="navbar navbar-expand navbar-dark bg-dark">
        <div className="container">
          <span className="navbar-brand mb-0 h1">farc admin</span>
          <div className="navbar-nav">
            <NavLink to="/storages" className={navLinkClass}>
              Storages
            </NavLink>
            <NavLink to="/channels" className={navLinkClass}>
              Channels
            </NavLink>
            <NavLink to="/player" className={navLinkClass}>
              Player
            </NavLink>
          </div>
        </div>
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
