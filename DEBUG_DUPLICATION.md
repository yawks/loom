# Debug de la duplication de fichiers

## Logs à surveiller dans `wails.log`

Pour identifier pourquoi des fichiers apparaissent en triple, surveillez ces logs dans l'ordre :

### 1. Conversion des messages Slack (`convertSlackMessage`)

Cherchez les logs qui commencent par `SlackProvider.convertSlackMessage` :

- **`Processing X files, Y attachments`** : Nombre de fichiers et attachements dans le message original
- **`Processing file #N`** : Chaque fichier traité avec ses détails (ID, URL, Name, Size, MimeType)
- **`SKIPPING duplicate file #N`** : Fichiers ignorés car déjà vus (par ID ou URL)
- **`Added attachment`** : Chaque attachement ajouté avec son Type, URL, Name
- **`After processing Files: X attachments`** : Nombre d'attachements après traitement des fichiers
- **`Added attachment from msg.Attachments`** : Attachements ajoutés depuis `msg.Attachments` (images)
- **`SKIPPING attachment #N`** : Attachements ignorés car déjà traités
- **`Final attachments count: X`** : Nombre final d'attachements avant sérialisation JSON
- **`Serialized X attachments to JSON`** : Confirmation de la sérialisation

**Ce qu'il faut vérifier** :
- Si un même fichier apparaît plusieurs fois dans `Processing file #N` avec le même ID/URL
- Si `Final attachments count` est supérieur au nombre de fichiers dans le message original
- Si des fichiers sont ignorés alors qu'ils ne devraient pas l'être

### 2. Stockage des messages (`storeMessagesForConversation`)

Cherchez les logs qui commencent par `SlackProvider.storeMessagesForConversation` :

- **`Will create new message`** : Nouveaux messages avec la longueur des attachements
- **`Will update message`** : Messages mis à jour avec comparaison des longueurs d'attachements
- **`SKIPPING duplicate message in batch`** : Messages dupliqués dans le même batch
- **`Batch inserted X new messages`** : Messages insérés en batch
- **`Stored X new, Y updated messages`** : Résumé final

**Ce qu'il faut vérifier** :
- Si un même `ProtocolMsgID` apparaît plusieurs fois dans le batch
- Si les attachements sont écrasés lors de la mise à jour (longueur passe de X à 0)

### 3. Logs frontend (console du navigateur)

Dans la console du navigateur, cherchez :

- **`[MessageAttachments] Parsed attachments: X`** : Nombre d'attachements parsés
- **`[MessageAttachments] Removing duplicate attachment`** : Attachements dédupliqués côté frontend
- **`[MessageAttachments] Deduplicated attachments: X -> Y`** : Résultat de la déduplication frontend

**Ce qu'il faut vérifier** :
- Si la déduplication frontend supprime des doublons (X > Y)
- Si des attachements avec la même URL apparaissent plusieurs fois

## Scénarios de duplication possibles

1. **Fichier apparaît plusieurs fois dans `msg.Files`** : Le backend devrait le détecter via `seenFileIDs` ou `seenURLs`
2. **Fichier dans `msg.Files` ET `msg.Attachments`** : Le backend devrait le détecter en comparant les URLs
3. **Message traité plusieurs fois** : Le backend devrait le détecter via `seenMsgIDs` dans `storeMessagesForConversation`
4. **Attachements écrasés lors de la mise à jour** : Vérifier si `existingMsg.Attachments` est préservé

## Commandes pour filtrer les logs

```bash
# Voir tous les logs de conversion
grep "SlackProvider.convertSlackMessage" wails.log | tail -50

# Voir les fichiers dupliqués ignorés
grep "SKIPPING duplicate" wails.log | tail -20

# Voir le nombre final d'attachements
grep "Final attachments count" wails.log | tail -20

# Voir les messages dupliqués dans un batch
grep "SKIPPING duplicate message in batch" wails.log | tail -20
```



