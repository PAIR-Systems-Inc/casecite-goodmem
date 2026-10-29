import { AI_REVIEW_NOTICE } from '../notice';
import s from '../styles/pane.module.css';

/** The verify-before-relying line shown under every AI result in the pane. */
export function Notice() {
  return <p className={s.notice}>{AI_REVIEW_NOTICE}</p>;
}
