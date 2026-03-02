# zigboo - Feature TODO

Full feature list for controlling the Sonoff ZBDongle-E (EFR32MG21) and managing a
Zigbee network via EZSP/ASH.

## Milestone 1 - ASH/EZSP Foundation (Complete)

- [x] ASH transport (RST/RSTACK, DATA/ACK/NAK, CRC, byte stuffing, randomization)
- [x] EZSP version negotiation (legacy v4-v8, extended v9+)
- [x] `getNodeId`, `getEUI64` queries
- [x] CLI: `reset`, `version`, `info` commands

## Milestone 2 - NCP Configuration & Network Formation

### NCP Configuration

- [ ] `setConfigurationValue`/`getConfigurationValue` -- table sizes, stack profile, security level, max hops, buffer counts, timeouts
- [ ] `setPolicy`/`getPolicy` -- trust center policy, binding modification policy, key request policies
- [ ] `getValue`/`setValue` -- frame counters, security bitmask, CCA threshold, antenna mode
- [ ] `addEndpoint` -- register application endpoints with profile/cluster lists
- [ ] `resetNode` and `tokenFactoryReset` -- NCP reset and factory reset
- [ ] `readAndClearCounters`/`readCounters` -- packet stats, retries, errors
- [ ] `getLibraryStatus` -- check which NCP libraries are compiled in
- [ ] `getToken`/`setToken`, `getTokenData`/`setTokenData` -- persistent NCP storage
- [ ] `getMfgToken`/`setMfgToken` -- manufacturing/hardware calibration tokens

### Network Formation & Management

- [ ] `startScan` (energy mode) -- find quietest channel
- [ ] `startScan` (active mode) -- discover existing networks
- [ ] `formNetwork` -- create new Zigbee network (PAN ID, extended PAN ID, channel, TX power)
- [ ] `networkInit` -- resume previously formed network from NCP storage
- [ ] `networkState` -- query current network state (up/down/joining)
- [ ] `getNetworkParameters` -- read current PAN ID, channel, extended PAN ID, node type
- [ ] `leaveNetwork` -- cleanly leave the current network
- [ ] `findUnusedPanId` / `sendPanIdUpdate` -- PAN ID conflict resolution
- [ ] `setRadioChannel` / `setLogicalAndRadioChannel` -- channel change
- [ ] `setRadioPower` -- TX power control
- [ ] `setConcentrator` -- enable source routing for large networks

## Milestone 3 - EZSP Callback Processing

- [ ] Callback polling (`callback`/`noCallbacks`) -- poll for asynchronous NCP events
- [ ] `stackStatusHandler` -- network up/down/error notifications
- [ ] `incomingRouteRecordHandler` / `incomingRouteErrorHandler` -- routing events
- [ ] `idConflictHandler` -- short address collision detection
- [ ] `counterRolloverHandler` -- counter overflow notification
- [ ] `stackTokenChangedHandler` -- NCP token change notification

## Milestone 4 - Security & Key Management

### Initial Security Setup

- [ ] `setInitialSecurityState` -- network key, TC link key, security bitmask

### Network Key Rotation

- [ ] `broadcastNextNetworkKey` -- broadcast new network key encrypted with old key
- [ ] `broadcastNetworkKeySwitch` -- tell all devices to switch to new key
- [ ] `unicastNwkKeyUpdate` / `unicastCurrentNetworkKey` -- unicast key to sleepy devices
- [ ] `switchNetworkKeyHandler` callback -- key switch completion

### Link Key Management

- [ ] `exportKey` / `importKey` (v13+) or `getKey` / `setKeyTableEntry` (v4-v12)
- [ ] `exportLinkKeyByEui` / `importLinkKey` -- per-device link keys
- [ ] `findKeyTableEntry` / `eraseKeyTableEntry` / `clearKeyTable`
- [ ] Frame counter management -- read/set NWK and APS frame counters

### Install Code Provisioning

- [ ] `aesMmoHash` -- derive link key from install code
- [ ] `addTransientLinkKey` (v4-v12) / `importTransientKey` (v12+) -- load derived key
- [ ] Transient key timeout configuration

## Milestone 5 - Device Joining & Pairing

- [ ] `permitJoining` -- open/close network with configurable timeout
- [ ] `trustCenterJoinHandler` callback -- join/rejoin/leave notification
- [ ] `childJoinHandler` callback -- track direct children
- [ ] Trust center policy configuration -- allow/deny joins, require link key
- [ ] Well-known key join control
- [ ] Device interview on join -- query node descriptor, active endpoints, simple descriptors, basic cluster

## Milestone 6 - Messaging Layer

### APS Messaging

- [ ] `sendUnicast` -- send APS messages to specific devices
- [ ] `sendBroadcast` -- send to all devices or all routers
- [ ] `sendMulticast` -- send to a group of devices
- [ ] `incomingMessageHandler` callback -- receive all incoming APS messages
- [ ] `messageSentHandler` callback -- delivery confirmation/failure
- [ ] `maximumPayloadLength` -- query available APS payload size
- [ ] `sendRawMessage` / `sendRawMessageExtended` -- raw 802.15.4 frame injection
- [ ] `macPassthroughMessageHandler` -- raw MAC frame reception

### ZCL Frame Construction

- [ ] ZCL frame header encoding (frame control, sequence number, command ID)
- [ ] ZCL payload serialization (attribute IDs, data types, values)
- [ ] APS frame construction (profile ID, cluster ID, source/destination endpoints)
- [ ] ZCL default response handling

## Milestone 7 - ZCL Cluster Support

### Core Attribute Operations

- [ ] Read Attributes (ZCL `0x00`)
- [ ] Write Attributes (ZCL `0x02`)
- [ ] Write Attributes Undivided (ZCL `0x03`)
- [ ] Write Attributes No Response (ZCL `0x05`)
- [ ] Discover Attributes (ZCL `0x0C`)
- [ ] Configure Reporting (ZCL `0x06`)
- [ ] Read Reporting Configuration (ZCL `0x08`)
- [ ] Report Attributes handler (ZCL `0x0A`)

### Cluster Implementations

- [ ] Basic (0x0000) -- device info, manufacturer name, model ID
- [ ] Power Configuration (0x0001) -- battery level
- [ ] On/Off (0x0006) -- switches, lights, plugs
- [ ] Level Control (0x0008) -- dimmers
- [ ] Color Control (0x0300) -- hue/sat, XY, color temp
- [ ] Temperature Measurement (0x0402)
- [ ] Humidity (0x0405)
- [ ] Pressure (0x0403)
- [ ] Illuminance (0x0400)
- [ ] Occupancy Sensing (0x0406) -- motion sensors
- [ ] IAS Zone (0x0500) -- security sensors (door/window, motion, smoke)
- [ ] IAS WD (0x0502) -- sirens/alarms
- [ ] Electrical Measurement (0x0B04) -- power monitoring
- [ ] Metering (0x0702) -- energy metering
- [ ] Thermostat (0x0201) -- HVAC control
- [ ] Fan Control (0x0202)
- [ ] Door Lock (0x0101)
- [ ] Window Covering (0x0102) -- blinds/shades

## Milestone 8 - Device Management

### Device Removal

- [ ] `removeDevice` -- send ZDO Leave Request to force-remove a device

### Table Management

- [ ] `getChildData` / `setChildData` -- child table inspection/modification
- [ ] `getNeighbor` / `neighborCount` -- neighbor table
- [ ] `getRouteTableEntry` -- routing table
- [ ] `getSourceRouteTableEntry` / `getSourceRouteTableTotalSize` / `getSourceRouteTableFilledSize` -- source route table
- [ ] Address table -- EUI-64 to short address mapping (`lookupNodeIdByEui64`, `lookupEui64ByNodeId`, etc.)

### Binding Table

- [ ] `setBinding` / `getBinding` / `deleteBinding` / `clearBindingTable`
- [ ] `bindingIsActive` -- check if binding is active
- [ ] `remoteSetBindingHandler` / `remoteDeleteBindingHandler` callbacks

## Milestone 9 - Group & Scene Management

### Groups

- [ ] Local multicast table (`setMulticastTableEntry` / `getMulticastTableEntry`)
- [ ] ZCL Groups cluster (0x0004) -- add/remove/view groups on devices

### Scenes

- [ ] ZCL Scenes cluster (0x0005) -- add/store/recall/remove scenes

## Milestone 10 - ZDO Commands

- [ ] IEEE Address Request / NWK Address Request
- [ ] Device Announce handler
- [ ] Node Descriptor Request
- [ ] Power Descriptor Request
- [ ] Simple Descriptor Request
- [ ] Active Endpoints Request
- [ ] Match Descriptor Request
- [ ] Bind / Unbind Requests
- [ ] End Device Bind
- [ ] LQI Table Request (neighbor table for topology mapping)
- [ ] Routing Table Request
- [ ] Network Update Request

## Milestone 11 - Green Power

- [ ] `gpSinkTableInit` -- initialize GP sink table
- [ ] `gpProxyTableProcessGpPairing` / `gpProxyTableGetEntry` / `gpProxyTableLookup` -- proxy table
- [ ] `gpSinkTableGetEntry` / `gpSinkTableSetEntry` / `gpSinkTableRemoveEntry` -- sink table
- [ ] `dGpSend` -- send GP frames
- [ ] `gpepIncomingMessageHandler` callback -- receive GP messages
- [ ] `gpSinkCommission` -- commission GP devices

## Milestone 12 - Touchlink / ZLL Commissioning

- [ ] `zllStartScan` -- discover nearby Touchlink-capable devices
- [ ] `zllNetworkOps` -- form/join via proximity
- [ ] `zllSetInitialSecurityState` -- ZLL security setup
- [ ] `zllGetTokens` / `zllSetDataToken` / `zllClearTokens` -- ZLL token management

## Milestone 13 - OTA Firmware Updates

- [ ] OTA server -- serve firmware images via ZCL OTA Upgrade cluster (0x0019)
- [ ] Image notification -- notify devices of available updates
- [ ] Block transfer -- handle Image Block Request/Response
- [ ] Upgrade management -- control when devices apply updates

## Milestone 14 - Network Backup & Restore

- [ ] Export network state -- params, network key, all link keys, frame counters, child table, binding table
- [ ] Restore to new coordinator -- set config, import keys with correct frame counters, `networkInit`
- [ ] Coordinator migration -- move network to a new dongle without re-pairing devices

## Milestone 15 - Diagnostics & Monitoring

- [ ] Stack counters -- packet counts, retries, failures, buffer usage
- [ ] Neighbor/route table inspection -- network topology visualization
- [ ] LQI/RSSI monitoring -- link quality between devices (via ZDO LQI table requests)
- [ ] Energy scan -- spectrum analysis of 2.4 GHz channels
- [ ] NCP keepalive (`nop`/`echo`) -- connection health monitoring
- [ ] Debug logging -- EZSP frame dumps, ASH frame traces

## Milestone 16 - Device Database & State Management

- [ ] Device registry -- track joined devices (EUI-64, short ID, model, manufacturer, endpoints, clusters)
- [ ] State cache -- last known attribute values for each device
- [ ] Availability tracking -- detect offline devices via missed polls/reports

## Milestone 17 - External Interfaces

- [ ] JSON output mode for CLI -- machine-readable output
- [ ] MQTT bridge -- publish device state, subscribe to commands
- [ ] REST / WebSocket API -- programmatic control
- [ ] Event stream -- real-time device events (joins, messages, state changes)
- [ ] Web UI -- device management dashboard
