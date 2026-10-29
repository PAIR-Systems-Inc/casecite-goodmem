import { useEffect, useId } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useDeviceApprovalStore } from '../stores/deviceApprovalStore';
import { useAuthStore } from '../stores/authStore';
import { Icon } from '../components/shared/Icon';
import { formatDeviceUserCode } from '../utils/deviceCode';
import s from './DeviceApprovalView.module.css';

/**
 * `/device?user_code=XXXX-XXXX`: approve or deny a sign-in the Word add-in is
 * asking for. Reached signed in (the auth overlay handles the rest), so the
 * decision is made by the account that will be shared with the add-in.
 */
function DeviceApprovalView() {
  const id = useId();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const user = useAuthStore((st) => st.user);
  const { userCode, status, error, decidedCode, setUserCode, prefill, approve, deny, reset } =
    useDeviceApprovalStore();

  const codeParam = params.get('user_code');
  useEffect(() => {
    prefill(codeParam);
  }, [codeParam, prefill]);
  useEffect(() => () => reset(), [reset]);

  const busy = status === 'submitting';
  const decided = status === 'approved' || status === 'denied';

  return (
    <div className={s.page}>
      <div className={s.card}>
        <div className={s.iconCircle}>
          <Icon
            name={status === 'approved' ? 'Check' : status === 'denied' ? 'X' : 'MonitorSmartphone'}
            size={28}
            className={s.icon}
          />
        </div>

        {!decided && (
          <>
            <h2 className={s.heading}>Approve a sign-in from Word?</h2>
            <p className={s.text}>
              The CaseCite add-in in Word is showing a code. Enter it here to let that add-in
              work as <strong>{user?.email ?? 'your account'}</strong>. Only approve a code you
              are looking at yourself; the add-in's session appears under Settings → Security →
              Sessions as “Word add-in” and can be ended there.
            </p>
            <form className={s.form} onSubmit={(e) => { e.preventDefault(); approve(); }}>
              {error && <div className={s.errorBox} role="alert">{error}</div>}
              <div>
                <label htmlFor={`${id}-code`} className={s.label}>Code shown in Word</label>
                <input
                  id={`${id}-code`}
                  className={s.codeInput}
                  value={formatDeviceUserCode(userCode)}
                  onChange={(e) => setUserCode(e.target.value)}
                  placeholder="XXXX-XXXX"
                  autoComplete="off"
                  autoCapitalize="characters"
                  spellCheck={false}
                  maxLength={9}
                  autoFocus
                />
              </div>
              <div className={s.actions}>
                <button type="submit" className={s.primaryButton} disabled={busy}>
                  {busy ? 'Working…' : 'Approve sign-in'}
                </button>
                <button type="button" className={s.secondaryButton} disabled={busy} onClick={deny}>
                  Deny
                </button>
              </div>
            </form>
          </>
        )}

        {status === 'approved' && (
          <>
            <h2 className={s.heading}>Approved</h2>
            <p className={s.text}>
              Code <span className={s.codeChip}>{decidedCode}</span> is approved. Word will finish
              signing in on its own within a few seconds; you can close this page.
            </p>
            <div className={s.actions}>
              <button type="button" className={s.primaryButton} onClick={() => navigate('/research')}>
                Back to CaseCite
              </button>
              <button type="button" className={s.secondaryButton} onClick={reset}>
                Approve another code
              </button>
            </div>
          </>
        )}

        {status === 'denied' && (
          <>
            <h2 className={s.heading}>Denied</h2>
            <p className={s.text}>
              Code <span className={s.codeChip}>{decidedCode}</span> was refused. The add-in will
              be told the sign-in was denied; nothing was shared.
            </p>
            <div className={s.actions}>
              <button type="button" className={s.primaryButton} onClick={() => navigate('/research')}>
                Back to CaseCite
              </button>
              <button type="button" className={s.secondaryButton} onClick={reset}>
                Enter another code
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

export default DeviceApprovalView;
