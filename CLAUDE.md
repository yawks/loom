# Loom — Architecture frontend

## Vue d'ensemble

Application desktop Wails (Go backend + React frontend). Le frontend communique avec le backend via les bindings Wails générés dans `frontend/wailsjs/`.

## Architecture de la vue messages

La vue messages était historiquement un seul fichier `MessageList.tsx` de 2700+ lignes. Elle a été découpée en couches distinctes :

```
src/
├── lib/
│   └── messageUtils.ts          — Utilitaires purs (pas de hooks React)
├── hooks/
│   ├── useMessageData.ts        — Fetch + traitement des données
│   ├── useFileUpload.ts         — Upload fichiers + drag & drop
│   └── useMessageEdit.ts        — État inline edit + navigation clavier
└── components/
    ├── MessageList.tsx           — Orchestrateur (~280 lignes)
    ├── MessageBubbleItem.tsx     — Rendu d'un message layout "bubble"
    ├── MessageIRCItem.tsx        — Rendu d'un message layout "irc"
    ├── MessageDateSeparator.tsx  — Séparateur de date
    ├── MessageUnreadDivider.tsx  — Séparateur "nouveaux messages"
    └── MessageThreadPreview.tsx  — Aperçu du dernier message d'un thread
```

### `src/lib/messageUtils.ts`

Fonctions pures sans dépendance React. Partagées entre `MessageList`, `MessageBubbleItem`, `MessageIRCItem`, et `ThreadView`.

- `getMessageDomId(message)` — ID stable pour le DOM (protocolMsgId > message-{id} > ts-{timestamp})
- `isDifferentDay(date1, date2)` — comparaison de dates pour les séparateurs
- `formatDateSeparator(date, t)` — label localisé (aujourd'hui / hier / lundi 5 mai…)
- `getColorFromString(str)` — couleur HSL déterministe depuis un userId (pour l'affichage IRC)
- `getSenderDisplayName(senderName, senderId, isFromMe, t)` — nom affiché, avec formatage des numéros WhatsApp

### `src/hooks/useMessageData.ts`

Encapsule toute la logique de données, sans aucun JSX.

- `useInfiniteQuery` pour la pagination (50 messages par page, scroll vers le haut = charger plus)
- Déduplication par `protocolMsgId`
- Séparation messages principaux / threads (`threadsByParent`)
- Chargement des noms de participants via `GetParticipantNames`
- Synchronisation avec `useMessageReadStore` (sync + cleanup des messages obsolètes)
- Invalidation des queries de tri après chargement

Signature : `useMessageData(conversationId: string, isGroupFromProvider: boolean)`

### `src/hooks/useFileUpload.ts`

Tout ce qui touche aux fichiers, inclus dans le même hook car les deux flux (drag&drop et retry/delete local) manipulent le cache React Query.

- Gestion de l'état drag (`isDragging`, `pendingFiles`, `pendingFilePaths`)
- `handleFileUpload` : priorité aux chemins fichiers (`SendFileFromPath`), fallback FileReader JS, fallback clipboard Go
- Compression d'images avant envoi (>1 Mo → JPEG 1600px max)
- `handleRetrySend` / `handleDeleteLocalMessage` : mise à jour optimiste du cache

Signature : `useFileUpload(conversationId: string)`

### `src/hooks/useMessageEdit.ts`

État et comportements de l'édition inline de messages.

- `editingMessageId`, `editingText`, `originalEditText`
- `editingInputRef` : ref passée au `<Input>` dans `MessageBubbleItem` pour l'écoute clavier en phase capture
- `handleNavigateToEdit(direction, returnFocusToInput?)` : navigation haut/bas entre les messages envoyés (ArrowUp = plus ancien, ArrowDown = plus récent, puis retour au champ de saisie)
- Écoute clavier en phase capture sur l'input d'édition (pour intercepter avant Virtuoso)

Signature : `useMessageEdit({ messages, conversationId, showToast, t })`

### `MessageList.tsx` — Orchestrateur

Responsabilités restantes dans le composant principal :
- Appel des trois hooks + assemblage des props
- Gestion window focus/blur (pour le marquage "lu")
- Effet de marquage de conversation comme lue (`MarkConversationAsRead`)
- Séparateur "nouveaux messages" (calcul + auto-dismiss 10s)
- Confirmation de suppression (`AlertDialog`)
- `handleReaction` avec mise à jour optimiste du cache
- Interface `MessageHandlers` passée aux deux layouts

### `MessageBubbleItem` / `MessageIRCItem`

Reçoivent les mêmes props via un objet `handlers: MessageHandlers` (défini dans `MessageBubbleItem.tsx` et réexporté). La différence principale :
- **Bubble** : avatar + timestamp à gauche/droite selon `isFromMe`, fond coloré, `editingInputRef` attaché pour la navigation clavier ArrowUp/Down
- **IRC** : colonne gauche fixe 60px, regroupement des messages consécutifs du même expéditeur (< 5 min), couleur par expéditeur via `getColorFromString`

Les deux partagent `MessageDateSeparator`, `MessageUnreadDivider`, `MessageThreadPreview`.

### `ThreadView.tsx`

Utilise maintenant `getColorFromString` et `getSenderDisplayName` depuis `@/lib/messageUtils` au lieu de les redéfinir localement.

## Conventions

- Les layouts "bubble" et "irc" sont des branches mutuellement exclusives dans `Virtuoso.itemContent`
- `MessageHandlers` est l'interface partagée pour tous les callbacks d'interaction — la passer en prop `handlers` évite l'explosion de props individuelles
- Les mises à jour optimistes du cache React Query suivent le pattern : `queryClient.setQueryData` immédiat → appel API → rollback si erreur
- `getMessageDomId` est la source de vérité pour les IDs DOM des messages (utilisé partout pour le scroll, le marquage lu, etc.)
