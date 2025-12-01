import type { models } from "../../wailsjs/go/models";
import { timeToDate } from "./utils";

export type InferredReceiptType = "delivery" | "read";

export interface InferredReceipt {
  userId: string;
  receiptType: InferredReceiptType;
  timestamp: number;
  isInferred: true; // Flag to distinguish from actual receipts
}

/**
 * Infer message status for DM (1-on-1) conversations.
 * Rule: If the other user has replied or reacted to ANY message,
 * infer that they have read all previous messages from the current user.
 */
export function inferDMStatus(
  messages: models.Message[],
  currentUserId: string,
  targetMessage: models.Message
): InferredReceipt[] {
  if (!messages || messages.length === 0) {
    return [];
  }

  // Find the other user (not the current user)
  const otherUserId = messages.find(
    (m) => m.senderId !== currentUserId
  )?.senderId;

  if (!otherUserId) {
    return []; // No other user found
  }

  // Find the latest activity (message or reaction) from the other user
  let latestActivityTimestamp = 0;

  // Check messages from other user
  for (const msg of messages) {
    if (msg.senderId === otherUserId) {
      const msgTime = timeToDate(msg.timestamp).getTime();
      if (msgTime > latestActivityTimestamp) {
        latestActivityTimestamp = msgTime;
      }
    }
  }

  // Check reactions from other user on any message
  for (const msg of messages) {
    if (msg.reactions) {
      for (const reaction of msg.reactions) {
        if (reaction.userId === otherUserId) {
          const reactionTime = timeToDate(reaction.createdAt).getTime();
          if (reactionTime > latestActivityTimestamp) {
            latestActivityTimestamp = reactionTime;
          }
        }
      }
    }
  }

  // If no activity from other user, no inference
  if (latestActivityTimestamp === 0) {
    return [];
  }

  // If target message is from current user and was sent before the latest activity,
  // infer that it was read
  if (
    targetMessage.senderId === currentUserId &&
    timeToDate(targetMessage.timestamp).getTime() < latestActivityTimestamp
  ) {
    return [
      {
        userId: otherUserId,
        receiptType: "read",
        timestamp: latestActivityTimestamp,
        isInferred: true,
      },
    ];
  }

  return [];
}

/**
 * Infer message status for group conversations.
 * Rules:
 * - If at least 1 participant has replied/reacted: upgrade "sent" to "delivered" for that participant
 * - If ALL participants have replied/reacted: upgrade to "read" for those participants
 */
export function inferGroupStatus(
  messages: models.Message[],
  groupParticipants: models.GroupParticipant[] | undefined,
  currentUserId: string,
  targetMessage: models.Message
): InferredReceipt[] {
  if (!messages || messages.length === 0 || !groupParticipants) {
    return [];
  }

  // Get all participants except current user
  const otherParticipants = groupParticipants.filter(
    (p) => p.userId !== currentUserId
  );

  if (otherParticipants.length === 0) {
    return [];
  }

  // Track latest activity timestamp for each participant
  const participantActivity = new Map<string, number>();

  // Check messages from each participant
  for (const msg of messages) {
    if (msg.senderId !== currentUserId) {
      const msgTime = timeToDate(msg.timestamp).getTime();
      const current = participantActivity.get(msg.senderId) || 0;
      if (msgTime > current) {
        participantActivity.set(msg.senderId, msgTime);
      }
    }
  }

  // Check reactions from each participant
  for (const msg of messages) {
    if (msg.reactions) {
      for (const reaction of msg.reactions) {
        if (reaction.userId !== currentUserId) {
          const reactionTime = timeToDate(reaction.createdAt).getTime();
          const current = participantActivity.get(reaction.userId) || 0;
          if (reactionTime > current) {
            participantActivity.set(reaction.userId, reactionTime);
          }
        }
      }
    }
  }

  // If target message is not from current user, no inference needed
  if (targetMessage.senderId !== currentUserId) {
    return [];
  }

  const targetMessageTime = timeToDate(targetMessage.timestamp).getTime();
  const inferredReceipts: InferredReceipt[] = [];

  // Count how many participants have activity after this message
  let participantsWithActivity = 0;

  for (const participant of otherParticipants) {
    const activityTime = participantActivity.get(participant.userId);
    if (activityTime && activityTime > targetMessageTime) {
      participantsWithActivity++;
    }
  }

  // If no one has activity, no inference
  if (participantsWithActivity === 0) {
    console.log('[Inference] No participant activity found for message', {
      targetMessageTime: new Date(targetMessageTime),
      participantActivity: Array.from(participantActivity.entries())
    });
    return [];
  }

  // Determine status based on activity:
  // - If ALL participants have activity → "read" for all
  // - If at least 1 has activity → "delivered" for all (assumption: if one received it, all did)
  const allParticipantsActive = participantsWithActivity === otherParticipants.length;
  const defaultReceiptType: InferredReceiptType = allParticipantsActive ? "read" : "delivery";

  console.log('[Inference] Group status inference:', {
    targetMessageTime: new Date(targetMessageTime),
    participantsWithActivity,
    totalParticipants: otherParticipants.length,
    allParticipantsActive,
    defaultReceiptType,
    participantActivity: Array.from(participantActivity.entries()).map(([userId, time]) => ({
      userId,
      time: new Date(time)
    }))
  });

  // Create inferred receipts for ALL participants
  for (const participant of otherParticipants) {
    const activityTime = participantActivity.get(participant.userId);

    // Use participant's actual activity time if available, otherwise use the earliest activity time
    const timestamp = activityTime && activityTime > targetMessageTime 
      ? activityTime 
      : Math.max(...Array.from(participantActivity.values()).filter(t => t > targetMessageTime));

    inferredReceipts.push({
      userId: participant.userId,
      receiptType: defaultReceiptType,
      timestamp,
      isInferred: true,
    });
  }

  console.log('[Inference] Created inferred receipts:', inferredReceipts.length);

  return inferredReceipts;
}

/**
 * Merge actual receipts with inferred receipts.
 * Actual receipts always take precedence.
 */
export function mergeReceipts(
  actualReceipts: models.MessageReceipt[] | undefined,
  inferredReceipts: InferredReceipt[]
): models.MessageReceipt[] {
  const merged: models.MessageReceipt[] = [];

  // Add all actual receipts first
  if (actualReceipts) {
    merged.push(...actualReceipts);
  }

  // Track which users already have actual receipts
  const usersWithActualReceipts = new Set(
    actualReceipts?.map((r) => r.userId) || []
  );

  // Add inferred receipts only for users without actual receipts
  for (const inferred of inferredReceipts) {
    if (!usersWithActualReceipts.has(inferred.userId)) {
      // Create a partial receipt object that matches the MessageReceipt interface
      // We only need the fields used by the status display logic
      merged.push({
        userId: inferred.userId,
        receiptType: inferred.receiptType,
        timestamp: inferred.timestamp,
      } as unknown as models.MessageReceipt);
    }
  }

  return merged;
}
