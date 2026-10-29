import React from 'react';
import { Icon } from './Icon';
import { activateOnKey } from './activateOnKey';
import { formatFileSize } from '../../utils';
import type { DocumentTreeItem } from '../../types';
import localStyles from './DocumentTreeItemRow.module.css';

interface DocumentTreeItemRowProps {
  styles: Record<string, string>;
  item: DocumentTreeItem;
  depth: number;
  expandedFolders: Set<string>;
  selectedItems: Set<string>;
  searchFilter: string;
  toggleFolder: (path: string) => void;
  toggleSelect: (item: DocumentTreeItem) => void;
}

function DocumentTreeItemRow({ styles, item, depth, expandedFolders, selectedItems, searchFilter, toggleFolder, toggleSelect }: DocumentTreeItemRowProps) {
  const isExpanded = expandedFolders.has(item.path);
  const isSelected = selectedItems.has(item.type === 'folder' ? item.path : item.id);
  const matchesSearch = !searchFilter ||
    item.name.toLowerCase().includes(searchFilter.toLowerCase());

  const hasMatchingChildren = item.type === 'folder' && item.children?.some(
    (child: DocumentTreeItem) => child.name.toLowerCase().includes(searchFilter.toLowerCase()) ||
    (child.type === 'folder' && child.children?.some((c: DocumentTreeItem) =>
      c.name.toLowerCase().includes(searchFilter.toLowerCase())
    ))
  );

  if (searchFilter && !matchesSearch && !hasMatchingChildren) return null;

  return (
    <div>
      <div
        className={`${styles.treeItem} ${localStyles.treeItemIndent} ${isSelected ? styles.treeItemSelected : ''}`}
        style={{ '--depth-indent': `${depth * 20}px` } as React.CSSProperties}
      >
        {item.type === 'folder' ? (
          <>
            <span
              role="button"
              tabIndex={0}
              aria-label={isExpanded ? `Collapse ${item.name}` : `Expand ${item.name}`}
              aria-expanded={isExpanded}
              onClick={() => toggleFolder(item.path)}
              onKeyDown={activateOnKey(() => toggleFolder(item.path))}
              className={styles.folderToggle}
            >
              <Icon name={isExpanded ? 'ChevronDown' : 'ChevronRight'} size={16} className={styles.iconGray500} />
            </span>
            <input
              aria-label={`Select folder ${item.name}`}
              type="checkbox"
              checked={isSelected}
              onChange={() => toggleSelect(item)}
              className={styles.checkbox}
            />
            <Icon name="Folder" size={18} style={{color: isExpanded ? 'var(--primary-500)' : 'var(--gray-500)'}} />
            <span className={styles.folderName}>{item.name}</span>
            <span className={styles.docCount}>
              {item.document_count} docs
            </span>
          </>
        ) : (
          <>
            <span className={styles.fileSpacer} />
            <input
              aria-label={`Select ${item.name}`}
              type="checkbox"
              checked={isSelected}
              onChange={() => toggleSelect(item)}
              className={styles.checkbox}
            />
            <Icon
              name={item.name.endsWith('.pdf') ? 'FileText' : item.name.endsWith('.docx') ? 'FileEdit' : 'File'}
              size={16}
              className={styles.iconGray500}
            />
            <span className={styles.fileName}>
              {item.name}
            </span>
            {item.size && (
              <span className={styles.fileSize}>
                {formatFileSize(item.size)}
              </span>
            )}
          </>
        )}
      </div>
      {item.type === 'folder' && isExpanded && item.children?.map((child: DocumentTreeItem) => (
        <DocumentTreeItemRow
          key={child.id || child.path}
          styles={styles}
          item={child}
          depth={depth + 1}
          expandedFolders={expandedFolders}
          selectedItems={selectedItems}
          searchFilter={searchFilter}
          toggleFolder={toggleFolder}
          toggleSelect={toggleSelect}
        />
      ))}
    </div>
  );
}

export { DocumentTreeItemRow };
