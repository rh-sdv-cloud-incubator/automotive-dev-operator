import { useEffect, useRef, useState, useCallback } from 'react';

export interface SSEMessage {
  event: string;
  data: string;
  id?: string;
}

export interface SSEHookOptions {
  onMessage?: (message: SSEMessage) => void;
  onError?: (error: Event) => void;
  onOpen?: () => void;
  onClose?: () => void;
  autoReconnect?: boolean;
  reconnectInterval?: number;
  maxReconnectAttempts?: number;
}

export interface SSEHookReturn {
  isConnected: boolean;
  isConnecting: boolean;
  error: string | null;
  connect: () => void;
  disconnect: () => void;
  lastMessage: SSEMessage | null;
}

export const useSSE = (url: string | null, options: SSEHookOptions = {}): SSEHookReturn => {
  const {
    onMessage,
    onError,
    onOpen,
    onClose,
    autoReconnect = true,
    reconnectInterval = 3000,
    maxReconnectAttempts = 5,
  } = options;

  const [isConnected, setIsConnected] = useState(false);
  const [isConnecting, setIsConnecting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lastMessage, setLastMessage] = useState<SSEMessage | null>(null);

  const abortControllerRef = useRef<AbortController | null>(null);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const shouldReconnectRef = useRef(true);

  const cleanup = useCallback(() => {
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
      abortControllerRef.current = null;
    }
  }, []);

  const connect = useCallback(async () => {
    console.log('[useSSE] connect() called, url:', url, 'existing controller:', !!abortControllerRef.current);
    if (!url) {
      console.log('[useSSE] connect() aborted - no url');
      return;
    }

    cleanup();
    setIsConnecting(true);
    setError(null);
    shouldReconnectRef.current = true;

    try {
      const finalUrl = url.startsWith('http')
      ? url
      : `${window.location.origin}${url.startsWith('/') ? url : `/${url}`}`;
      console.log('[useSSE] Connecting to EventSource:', finalUrl);

      const eventSource = new EventSource(finalUrl, { withCredentials: true });
      abortControllerRef.current = { abort: () => eventSource.close() } as any;

      eventSource.onopen = () => {
        console.log('[useSSE] EventSource opened');
        setIsConnected(true);
        setIsConnecting(false);
        setError(null);
        reconnectAttemptsRef.current = 0;
        onOpen?.();
      };

      eventSource.onerror = (err) => {
        console.error('[useSSE] EventSource error:', err);
        setIsConnected(false);
        setIsConnecting(false);
        setError('Connection error');
        onError?.(err as Event);
        eventSource.close();

        if (shouldReconnectRef.current && autoReconnect && reconnectAttemptsRef.current < maxReconnectAttempts) {
          reconnectAttemptsRef.current++;
          reconnectTimeoutRef.current = setTimeout(() => {
            if (shouldReconnectRef.current) {
              connect();
            }
          }, reconnectInterval);
        }
      };

      eventSource.onmessage = (event) => {
        const message: SSEMessage = {
          event: 'message',
          data: event.data,
          id: event.lastEventId || undefined,
        };
        setLastMessage(message);
        onMessage?.(message);
      };

      // Handle custom event types
      const originalAddEventListener = eventSource.addEventListener.bind(eventSource);
      const eventTypes = new Set<string>();

      // Intercept addEventListener to track custom events
      (eventSource as any).addEventListener = (type: string, listener: any, options?: any) => {
        if (type !== 'message' && type !== 'error' && type !== 'open' && !eventTypes.has(type)) {
          eventTypes.add(type);
          originalAddEventListener(type, (event: MessageEvent) => {
            const message: SSEMessage = {
              event: type,
              data: event.data,
              id: event.lastEventId || undefined,
            };
            setLastMessage(message);
            onMessage?.(message);
          });
        }
        return originalAddEventListener(type, listener, options);
      };

      // Manually handle known event types for logs
      const knownEvents = ['connected', 'log', 'step', 'waiting', 'ping', 'completed', 'error', 'disconnected'];
      knownEvents.forEach(eventType => {
        eventSource.addEventListener(eventType, (event: MessageEvent) => {
          const message: SSEMessage = {
            event: eventType,
            data: event.data,
            id: event.lastEventId || undefined,
          };
          setLastMessage(message);
          onMessage?.(message);
        });
      });

    } catch (err) {
      console.error('[useSSE] EventSource setup error:', err);
      setIsConnected(false);
      setIsConnecting(false);
      setError(err instanceof Error ? err.message : 'Unknown error');
    }
  }, [url, onMessage, onError, onOpen, autoReconnect, reconnectInterval, maxReconnectAttempts, cleanup]);

  const disconnect = useCallback(() => {
    console.log('[useSSE] disconnect() called');
    shouldReconnectRef.current = false;
    cleanup();
    setIsConnected(false);
    setIsConnecting(false);
    onClose?.();
  }, [cleanup, onClose]);

  // Auto-connect when URL changes
  useEffect(() => {
    console.log('[useSSE] useEffect triggered, url:', url);
    if (url) {
      connect();
    } else {
      disconnect();
    }

    return () => {
      console.log('[useSSE] useEffect cleanup running');
      disconnect();
    };
  }, [url]); // eslint-disable-line react-hooks/exhaustive-deps

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      disconnect();
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return {
    isConnected,
    isConnecting,
    error,
    connect,
    disconnect,
    lastMessage,
  };
};

