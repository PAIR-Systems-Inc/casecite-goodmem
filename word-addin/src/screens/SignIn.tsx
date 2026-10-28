import { approvalUrl, openApprovalPage, useAddinStore } from '../store';
import s from '../styles/pane.module.css';

/** Device-code sign-in: show the code, send the person to the web app to approve it. */
export function SignIn() {
  const { serverUrl, signIn, startSignIn, cancelSignIn, setScreen } = useAddinStore();
  const waiting = signIn.status === 'waiting' && signIn.code;

  return (
    <div className={s.card}>
      <h2 className={s.heading}>Sign in to CaseCite</h2>
      {!serverUrl && (
        <p className={s.text}>
          First tell the add-in where your firm's CaseCite runs.{' '}
          <button type="button" className={s.link} onClick={() => setScreen('settings')}>
            Open settings
          </button>
        </p>
      )}

      {signIn.status === 'idle' || signIn.status === 'error' ? (
        <>
          <p className={s.text}>
            You will get a short code. Approve it in CaseCite in your browser, where you are already
            signed in; this pane then works as that account. Nothing is typed into Word.
          </p>
          {signIn.status === 'error' && (
            <p className={s.error} role="alert">
              {signIn.error}
            </p>
          )}
          <button type="button" className={s.primary} onClick={startSignIn} disabled={!serverUrl}>
            Get a sign-in code
          </button>
        </>
      ) : null}

      {signIn.status === 'starting' && (
        <p className={s.text}>
          <span className={s.spinner} />
          Asking {serverUrl} for a code…
        </p>
      )}

      {waiting && signIn.code && (
        <>
          <p className={s.text}>Enter this code in CaseCite:</p>
          <code className={s.code}>{signIn.code.user_code}</code>
          <p className={s.text}>
            A browser window should have opened. If it did not,{' '}
            <button
              type="button"
              className={s.link}
              onClick={() => openApprovalPage(approvalUrl(serverUrl, signIn.code!))}
            >
              open the approval page
            </button>
            . The code expires in {Math.round(signIn.code.expires_in / 60)} minutes.
          </p>
          <p className={s.muted}>
            <span className={s.spinner} />
            Waiting for approval…
          </p>
          <button type="button" className={s.secondary} onClick={cancelSignIn}>
            Cancel
          </button>
        </>
      )}
    </div>
  );
}
