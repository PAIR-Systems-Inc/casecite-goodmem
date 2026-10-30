import { useAuthStore } from '../stores/authStore';
import { Icon } from '../components';
import s from './AuthOverlay.module.css';

export function AuthOverlay() {
  const { setShowSignupModal, setShowLoginModal } = useAuthStore();

  return (
    <div className={s.overlay}>
      <img src="/pairsys-mark.svg" alt="PAIR Systems" className={s.welcomeMark} width="80" height="80" />
      <p className={s.eyebrow}>A PRIVATE LEGAL WORKSPACE</p>
      <h2 className={s.heading}>
        Welcome to CaseCite
      </h2>
      <p className={s.byline}>by PairSys</p>
      <p className={s.subtext}>
        Research with context. Review with confidence. Bring your documents,
        case law and drafting together in one workspace, on infrastructure you control.
      </p>
      <div className={s.buttonGroup}>
        <button
          onClick={() => setShowSignupModal(true)}
          className={s.primaryButton}
        >
          <Icon name="UserPlus" size={18} />
          Register
        </button>
        <button
          onClick={() => setShowLoginModal(true)}
          className={s.secondaryButton}
        >
          Sign In
        </button>
      </div>
    </div>
  );
}
