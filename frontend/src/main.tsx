import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import PublicDemoEntry from './PublicDemoEntry';
import './styles/fonts';
import './index.css';

const root = ReactDOM.createRoot(document.getElementById('root') as HTMLElement);
root.render(
  <BrowserRouter>
    <PublicDemoEntry />
  </BrowserRouter>
);
