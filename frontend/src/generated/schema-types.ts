export type Maybe<T> = T | null;
export type InputMaybe<T> = Maybe<T>;
/** All built-in and custom scalars, mapped to their actual values */
export type Scalars = {
  ID: { input: string; output: string; }
  String: { input: string; output: string; }
  Boolean: { input: boolean; output: boolean; }
  Int: { input: number; output: number; }
  Float: { input: number; output: number; }
  Upload: { input: unknown; output: unknown; }
};

export type AiChatMessage = {
  __typename?: 'AIChatMessage';
  RequiresConfirmation: Scalars['Boolean']['output'];
  Result?: Maybe<RowsResult>;
  Text: Scalars['String']['output'];
  Type: Scalars['String']['output'];
};

export type AiProvider = {
  __typename?: 'AIProvider';
  Icon?: Maybe<Scalars['String']['output']>;
  IsEnvironmentDefined: Scalars['Boolean']['output'];
  IsGeneric: Scalars['Boolean']['output'];
  Name: Scalars['String']['output'];
  ProviderId: Scalars['String']['output'];
  Type: Scalars['String']['output'];
};

export type AtomicWhereCondition = {
  ColumnType: Scalars['String']['input'];
  Key: Scalars['String']['input'];
  Operator: Scalars['String']['input'];
  Value: Scalars['String']['input'];
};

export type ChatInput = {
  Model: Scalars['String']['input'];
  PreviousConversation: Scalars['String']['input'];
  Query: Scalars['String']['input'];
  Token?: InputMaybe<Scalars['String']['input']>;
};

export type Column = {
  __typename?: 'Column';
  Comment?: Maybe<Scalars['String']['output']>;
  IsForeignKey: Scalars['Boolean']['output'];
  IsPrimary: Scalars['Boolean']['output'];
  Length?: Maybe<Scalars['Int']['output']>;
  MetadataFidelity: SourceMetadataFidelity;
  Name: Scalars['String']['output'];
  Precision?: Maybe<Scalars['Int']['output']>;
  ReferencedColumn?: Maybe<Scalars['String']['output']>;
  ReferencedTable?: Maybe<Scalars['String']['output']>;
  Scale?: Maybe<Scalars['Int']['output']>;
  Type: Scalars['String']['output'];
};

export type ColumnCreationCapabilities = {
  __typename?: 'ColumnCreationCapabilities';
  CheckMinMax: Scalars['Boolean']['output'];
  CheckValues: Scalars['Boolean']['output'];
  CompositePrimaryKey: Scalars['Boolean']['output'];
  DefaultValue: Scalars['Boolean']['output'];
  ForeignKey: Scalars['Boolean']['output'];
  Identity: Scalars['Boolean']['output'];
  Nullable: Scalars['Boolean']['output'];
  PrimaryKey: Scalars['Boolean']['output'];
  Types: Scalars['Boolean']['output'];
  Unique: Scalars['Boolean']['output'];
};

export type ColumnCreationLabels = {
  __typename?: 'ColumnCreationLabels';
  CheckMax: Scalars['String']['output'];
  CheckMin: Scalars['String']['output'];
  CheckValues: Scalars['String']['output'];
  DefaultValue: Scalars['String']['output'];
  ForeignKey: Scalars['String']['output'];
  Identity: Scalars['String']['output'];
  Nullable: Scalars['String']['output'];
  PrimaryKey: Scalars['String']['output'];
  Unique: Scalars['String']['output'];
};

export type ColumnDefinitionInput = {
  CheckMax?: InputMaybe<Scalars['Float']['input']>;
  CheckMin?: InputMaybe<Scalars['Float']['input']>;
  CheckValues?: InputMaybe<Array<Scalars['String']['input']>>;
  DefaultValue?: InputMaybe<Scalars['String']['input']>;
  ForeignKey?: InputMaybe<ForeignKeyDefinitionInput>;
  Identity: Scalars['Boolean']['input'];
  Name: Scalars['String']['input'];
  Nullable?: InputMaybe<Scalars['Boolean']['input']>;
  Primary: Scalars['Boolean']['input'];
  Type: Scalars['String']['input'];
  Unique: Scalars['Boolean']['input'];
};

export enum CopyDataMode {
  StructureAndData = 'STRUCTURE_AND_DATA',
  StructureOnly = 'STRUCTURE_ONLY'
}

export enum CopyExistingStrategy {
  Append = 'APPEND',
  Overwrite = 'OVERWRITE',
  Skip = 'SKIP'
}

export type CopySourceObjectsInput = {
  ExistingStrategy: CopyExistingStrategy;
  Mode: CopyDataMode;
  ObjectNames: Array<Scalars['String']['input']>;
  SourceParent?: InputMaybe<SourceObjectRefInput>;
  Target: SourceTargetConnectionInput;
  TargetSchema?: InputMaybe<Scalars['String']['input']>;
};

export type CopySourceObjectsResult = {
  __typename?: 'CopySourceObjectsResult';
  Details: Array<CopyTableDetail>;
  FailedCount: Scalars['Int']['output'];
  SkippedCount: Scalars['Int']['output'];
  Status: Scalars['Boolean']['output'];
  SucceededCount: Scalars['Int']['output'];
  TotalRowsCopied: Scalars['Int']['output'];
  TotalTables: Scalars['Int']['output'];
};

export type CopyTableDetail = {
  __typename?: 'CopyTableDetail';
  Message?: Maybe<Scalars['String']['output']>;
  Name: Scalars['String']['output'];
  RowsCopied: Scalars['Int']['output'];
  Status: CopyTableStatus;
};

export enum CopyTableStatus {
  Failed = 'FAILED',
  Skipped = 'SKIPPED',
  Success = 'SUCCESS'
}

export type CreationOptionDefinition = {
  __typename?: 'CreationOptionDefinition';
  Key: Scalars['String']['output'];
  Label: Scalars['String']['output'];
  Required: Scalars['Boolean']['output'];
  Values: Array<Scalars['String']['output']>;
};

export enum DataShape {
  Content = 'Content',
  Document = 'Document',
  Graph = 'Graph',
  Metadata = 'Metadata',
  Tabular = 'Tabular'
}

export type ExecuteFlywayInput = {
  Action: Scalars['String']['input'];
  BaselineOnMigrate?: InputMaybe<Scalars['Boolean']['input']>;
  Directory: Scalars['String']['input'];
  OutOfOrder?: InputMaybe<Scalars['Boolean']['input']>;
  Target: SourceTargetConnectionInput;
  TargetSchema?: InputMaybe<Scalars['String']['input']>;
  ValidateOnMigrate?: InputMaybe<Scalars['Boolean']['input']>;
};

export type FlywayDirectoryPreview = {
  __typename?: 'FlywayDirectoryPreview';
  Directory: Scalars['String']['output'];
  Error?: Maybe<Scalars['String']['output']>;
  Exists: Scalars['Boolean']['output'];
  Files: Array<FlywayMigrationFile>;
};

export type FlywayExecutionResult = {
  __typename?: 'FlywayExecutionResult';
  Action: Scalars['String']['output'];
  Command: Scalars['String']['output'];
  DurationMs: Scalars['Int']['output'];
  Error?: Maybe<Scalars['String']['output']>;
  Output: Scalars['String']['output'];
  Success: Scalars['Boolean']['output'];
};

export type FlywayInstallResult = {
  __typename?: 'FlywayInstallResult';
  Error?: Maybe<Scalars['String']['output']>;
  Output: Scalars['String']['output'];
  Status: FlywayStatus;
  Success: Scalars['Boolean']['output'];
};

export type FlywayMigrationFile = {
  __typename?: 'FlywayMigrationFile';
  Description: Scalars['String']['output'];
  Filename: Scalars['String']['output'];
  RelativePath: Scalars['String']['output'];
  SizeBytes: Scalars['Int']['output'];
  Type: Scalars['String']['output'];
  Version: Scalars['String']['output'];
};

export type FlywayStatus = {
  __typename?: 'FlywayStatus';
  BinaryPath?: Maybe<Scalars['String']['output']>;
  InstallMethod?: Maybe<Scalars['String']['output']>;
  Installed: Scalars['Boolean']['output'];
  Version?: Maybe<Scalars['String']['output']>;
};

export type ForeignKeyDefinition = {
  __typename?: 'ForeignKeyDefinition';
  Column: Scalars['String']['output'];
  Table: Scalars['String']['output'];
};

export type ForeignKeyDefinitionInput = {
  Column: Scalars['String']['input'];
  Table: Scalars['String']['input'];
};

export type GenerateChatTitleInput = {
  Endpoint?: InputMaybe<Scalars['String']['input']>;
  Model: Scalars['String']['input'];
  ModelType: Scalars['String']['input'];
  ProviderId?: InputMaybe<Scalars['String']['input']>;
  Query: Scalars['String']['input'];
  SessionId?: InputMaybe<Scalars['String']['input']>;
  Token?: InputMaybe<Scalars['String']['input']>;
};

export type GenerateChatTitleResponse = {
  __typename?: 'GenerateChatTitleResponse';
  Title: Scalars['String']['output'];
};

export type GraphUnit = {
  __typename?: 'GraphUnit';
  Relations: Array<GraphUnitRelationship>;
  Unit: SourceObject;
};

export type GraphUnitRelationship = {
  __typename?: 'GraphUnitRelationship';
  MetadataFidelity: SourceMetadataFidelity;
  Name: Scalars['String']['output'];
  Relationship: GraphUnitRelationshipType;
  SourceColumn?: Maybe<Scalars['String']['output']>;
  TargetColumn?: Maybe<Scalars['String']['output']>;
};

export enum GraphUnitRelationshipType {
  ManyToMany = 'ManyToMany',
  ManyToOne = 'ManyToOne',
  OneToMany = 'OneToMany',
  OneToOne = 'OneToOne',
  Unknown = 'Unknown'
}

export type HealthStatus = {
  __typename?: 'HealthStatus';
  Database: Scalars['String']['output'];
  Server: Scalars['String']['output'];
};

export type ImportColumnMapping = {
  Skip: Scalars['Boolean']['input'];
  SourceColumn: Scalars['String']['input'];
  TargetColumn?: InputMaybe<Scalars['String']['input']>;
};

export type ImportColumnMappingPreview = {
  __typename?: 'ImportColumnMappingPreview';
  SourceColumn: Scalars['String']['output'];
  TargetColumn: Scalars['String']['output'];
};

export enum ImportFileFormat {
  Csv = 'CSV',
  Excel = 'EXCEL'
}

export type ImportFileInput = {
  AllowAutoGenerated?: InputMaybe<Scalars['Boolean']['input']>;
  File: Scalars['Upload']['input'];
  Mapping: Array<ImportColumnMapping>;
  Mode: ImportMode;
  Options: ImportFileOptions;
  Ref: SourceObjectRefInput;
};

export type ImportFileOptions = {
  Delimiter?: InputMaybe<Scalars['String']['input']>;
  Format: ImportFileFormat;
  Sheet?: InputMaybe<Scalars['String']['input']>;
};

export enum ImportMode {
  Append = 'APPEND',
  Overwrite = 'OVERWRITE',
  Upsert = 'UPSERT'
}

export type ImportPreview = {
  __typename?: 'ImportPreview';
  AutoGeneratedColumns: Array<Scalars['String']['output']>;
  Columns: Array<Scalars['String']['output']>;
  Mapping?: Maybe<Array<ImportColumnMappingPreview>>;
  RequiresAllowAutoGenerated: Scalars['Boolean']['output'];
  Rows: Array<Array<Scalars['String']['output']>>;
  Sheet?: Maybe<Scalars['String']['output']>;
  Truncated: Scalars['Boolean']['output'];
  ValidationError?: Maybe<Scalars['String']['output']>;
};

export type ImportResult = {
  __typename?: 'ImportResult';
  Detail?: Maybe<Scalars['String']['output']>;
  Message: Scalars['String']['output'];
  Status: Scalars['Boolean']['output'];
};

export type ImportSqlInput = {
  File?: InputMaybe<Scalars['Upload']['input']>;
  Filename?: InputMaybe<Scalars['String']['input']>;
  Script?: InputMaybe<Scalars['String']['input']>;
};

export type MockDataDependencyAnalysis = {
  __typename?: 'MockDataDependencyAnalysis';
  Error?: Maybe<Scalars['String']['output']>;
  GenerationOrder: Array<Scalars['String']['output']>;
  Tables: Array<MockDataTableInfo>;
  TotalRows: Scalars['Int']['output'];
  Warnings: Array<Scalars['String']['output']>;
};

export type MockDataGenerationInput = {
  FkDensityRatio?: InputMaybe<Scalars['Int']['input']>;
  Method: Scalars['String']['input'];
  OverwriteExisting: Scalars['Boolean']['input'];
  Ref: SourceObjectRefInput;
  RowCount: Scalars['Int']['input'];
};

export type MockDataGenerationStatus = {
  __typename?: 'MockDataGenerationStatus';
  AmountGenerated: Scalars['Int']['output'];
  Details?: Maybe<Array<MockDataTableDetail>>;
};

export type MockDataTableDetail = {
  __typename?: 'MockDataTableDetail';
  RowsGenerated: Scalars['Int']['output'];
  Table: Scalars['String']['output'];
  UsedExistingData: Scalars['Boolean']['output'];
};

export type MockDataTableInfo = {
  __typename?: 'MockDataTableInfo';
  IsBlocked: Scalars['Boolean']['output'];
  RowsToGenerate: Scalars['Int']['output'];
  Table: Scalars['String']['output'];
  UsesExistingData: Scalars['Boolean']['output'];
};

export type Mutation = {
  __typename?: 'Mutation';
  AddSourceRow: StatusResponse;
  ClearSourceObjectData: StatusResponse;
  CopySourceObjects: CopySourceObjectsResult;
  CreateSourceObject: StatusResponse;
  CreateSourceObjectFromDefinition: StatusResponse;
  DeleteSourceRow: StatusResponse;
  DropSourceObject: StatusResponse;
  ExecuteConfirmedSQL: AiChatMessage;
  ExecuteFlyway: FlywayExecutionResult;
  ExportSourceConnection: Array<Record>;
  GenerateChatTitle: GenerateChatTitleResponse;
  GenerateMockData: MockDataGenerationStatus;
  ImportPreview: ImportPreview;
  ImportSQL: ImportResult;
  ImportSourceObjectFile: ImportResult;
  InstallFlyway: FlywayInstallResult;
  LoginOpsControlPlane: OpsControlPlaneStatus;
  LoginSource: StatusResponse;
  LoginWithSourceProfile: StatusResponse;
  Logout: StatusResponse;
  LogoutOpsControlPlane: StatusResponse;
  /** Recheck the diagnosis, repair history, validate, and retry migration once. */
  RepairSourceMigrations: SourceMigrationRepairResult;
  /** Deletes only reviewed duplicates in one transaction; rejects changes since the preview. */
  SourceCleanMigrationDuplicates: SourceMigrationDuplicateCleanup;
  SyncOpsControlPlaneProfiles: OpsControlPlaneStatus;
  TestSourceConnection: StatusResponse;
  UpdateSettings: StatusResponse;
  UpdateSourceObject: StatusResponse;
};


export type MutationAddSourceRowArgs = {
  ref: SourceObjectRefInput;
  values: Array<RecordInput>;
};


export type MutationClearSourceObjectDataArgs = {
  ref: SourceObjectRefInput;
};


export type MutationCopySourceObjectsArgs = {
  input: CopySourceObjectsInput;
};


export type MutationCreateSourceObjectArgs = {
  fields: Array<RecordInput>;
  name: Scalars['String']['input'];
  parent?: InputMaybe<SourceObjectRefInput>;
};


export type MutationCreateSourceObjectFromDefinitionArgs = {
  definition: SourceObjectDefinitionInput;
  parent?: InputMaybe<SourceObjectRefInput>;
};


export type MutationDeleteSourceRowArgs = {
  ref: SourceObjectRefInput;
  values: Array<RecordInput>;
};


export type MutationDropSourceObjectArgs = {
  ref: SourceObjectRefInput;
};


export type MutationExecuteConfirmedSqlArgs = {
  operationType: Scalars['String']['input'];
  query: Scalars['String']['input'];
};


export type MutationExecuteFlywayArgs = {
  input: ExecuteFlywayInput;
};


export type MutationExportSourceConnectionArgs = {
  id: Scalars['String']['input'];
  includeSecrets: Scalars['Boolean']['input'];
};


export type MutationGenerateChatTitleArgs = {
  input: GenerateChatTitleInput;
};


export type MutationGenerateMockDataArgs = {
  input: MockDataGenerationInput;
};


export type MutationImportPreviewArgs = {
  file: Scalars['Upload']['input'];
  options: ImportFileOptions;
  ref?: InputMaybe<SourceObjectRefInput>;
  useHeaderMapping?: InputMaybe<Scalars['Boolean']['input']>;
};


export type MutationImportSqlArgs = {
  input: ImportSqlInput;
};


export type MutationImportSourceObjectFileArgs = {
  input: ImportFileInput;
};


export type MutationLoginOpsControlPlaneArgs = {
  input: OpsControlPlaneLoginInput;
};


export type MutationLoginSourceArgs = {
  credentials: SourceLoginInput;
};


export type MutationLoginWithSourceProfileArgs = {
  profile: SourceProfileLoginInput;
};


export type MutationRepairSourceMigrationsArgs = {
  input: SourceMigrationRepairInput;
};


export type MutationSourceCleanMigrationDuplicatesArgs = {
  fingerprint: Scalars['String']['input'];
  input: SourceMigrationInput;
  object: Scalars['String']['input'];
  strategy: SourceDuplicateRetentionStrategy;
  version: Scalars['String']['input'];
};


export type MutationTestSourceConnectionArgs = {
  credentials: SourceLoginInput;
};


export type MutationUpdateSettingsArgs = {
  newSettings: SettingsConfigInput;
};


export type MutationUpdateSourceObjectArgs = {
  ref: SourceObjectRefInput;
  updatedColumns: Array<Scalars['String']['input']>;
  values: Array<RecordInput>;
};

export type ObjectCreationMetadata = {
  __typename?: 'ObjectCreationMetadata';
  ColumnCapabilities: ColumnCreationCapabilities;
  ColumnLabels: ColumnCreationLabels;
  ObjectKind: SourceObjectKind;
  RequiresColumns: Scalars['Boolean']['output'];
  Supported: Scalars['Boolean']['output'];
  TableCapabilities: TableCreationCapabilities;
  TableOptions: Array<CreationOptionDefinition>;
  TypeDefinitions: Array<TypeDefinition>;
};

export type OperationWhereCondition = {
  Children: Array<WhereCondition>;
};

export type OpsControlPlaneLoginInput = {
  Endpoint?: InputMaybe<Scalars['String']['input']>;
  Password: Scalars['String']['input'];
  Username: Scalars['String']['input'];
};

export type OpsControlPlaneStatus = {
  __typename?: 'OpsControlPlaneStatus';
  Authenticated: Scalars['Boolean']['output'];
  Endpoint: Scalars['String']['output'];
  EnvironmentCount: Scalars['Int']['output'];
  ProfileCount: Scalars['Int']['output'];
  Role?: Maybe<Scalars['String']['output']>;
  SyncedAt?: Maybe<Scalars['String']['output']>;
  Username?: Maybe<Scalars['String']['output']>;
};

export type Query = {
  __typename?: 'Query';
  AIModel: Array<Scalars['String']['output']>;
  AIProviders: Array<AiProvider>;
  AnalyzeMockDataDependencies: MockDataDependencyAnalysis;
  FlywayStatus: FlywayStatus;
  Health: HealthStatus;
  InspectFlywayDirectory: FlywayDirectoryPreview;
  MockDataMaxRowCount: Scalars['Int']['output'];
  OpsControlPlaneStatus: OpsControlPlaneStatus;
  RunSourceQuery: RowsResult;
  SSLStatus?: Maybe<SslStatus>;
  SettingsConfig: SettingsConfig;
  SourceColumns: Array<Column>;
  SourceColumnsBatch: Array<SourceObjectColumns>;
  SourceContent?: Maybe<SourceContent>;
  SourceFieldConstraints: Array<SourceFieldConstraints>;
  SourceFieldOptions: Array<Scalars['String']['output']>;
  SourceGraph: Array<GraphUnit>;
  /** Inspect migration history and validate scripts without executing callbacks. */
  SourceMigrationDiagnosis: SourceMigrationDiagnosis;
  /** Previews duplicate cleanup, ordered by the complete primary key; performs no writes. */
  SourceMigrationDuplicateCleanupPreview: SourceMigrationDuplicateCleanup;
  /** Reads all records involved in one diagnosed unique-index conflict without modifying data. */
  SourceMigrationDuplicateRows: RowsResult;
  SourceObject?: Maybe<SourceObject>;
  SourceObjectCreationMetadata: ObjectCreationMetadata;
  SourceObjects: Array<SourceObject>;
  SourceProfiles: Array<SourceProfile>;
  SourceQuerySuggestions: Array<SourceQuerySuggestion>;
  SourceRows: RowsResult;
  SourceSession?: Maybe<SourceSession>;
  SourceSessionMetadata?: Maybe<SourceSessionMetadata>;
  SourceTargetDatabases: Array<Scalars['String']['output']>;
  SourceTargetSchemas: Array<Scalars['String']['output']>;
  SourceTypes: Array<SourceType>;
  UpdateInfo: UpdateInfo;
  Version: Scalars['String']['output'];
};


export type QueryAiModelArgs = {
  endpoint?: InputMaybe<Scalars['String']['input']>;
  modelType: Scalars['String']['input'];
  providerId?: InputMaybe<Scalars['String']['input']>;
  token?: InputMaybe<Scalars['String']['input']>;
};


export type QueryAnalyzeMockDataDependenciesArgs = {
  fkDensityRatio?: InputMaybe<Scalars['Int']['input']>;
  ref: SourceObjectRefInput;
  rowCount: Scalars['Int']['input'];
};


export type QueryInspectFlywayDirectoryArgs = {
  directory: Scalars['String']['input'];
};


export type QueryRunSourceQueryArgs = {
  query: Scalars['String']['input'];
};


export type QuerySourceColumnsArgs = {
  ref: SourceObjectRefInput;
};


export type QuerySourceColumnsBatchArgs = {
  refs: Array<SourceObjectRefInput>;
};


export type QuerySourceContentArgs = {
  ref: SourceObjectRefInput;
};


export type QuerySourceFieldConstraintsArgs = {
  ref: SourceObjectRefInput;
};


export type QuerySourceFieldOptionsArgs = {
  fieldKey: Scalars['String']['input'];
  sourceType: Scalars['String']['input'];
  values?: InputMaybe<Array<RecordInput>>;
};


export type QuerySourceGraphArgs = {
  ref?: InputMaybe<SourceObjectRefInput>;
};


export type QuerySourceMigrationDiagnosisArgs = {
  input: SourceMigrationInput;
};


export type QuerySourceMigrationDuplicateCleanupPreviewArgs = {
  input: SourceMigrationInput;
  object: Scalars['String']['input'];
  strategy: SourceDuplicateRetentionStrategy;
  version: Scalars['String']['input'];
};


export type QuerySourceMigrationDuplicateRowsArgs = {
  input: SourceMigrationInput;
  object: Scalars['String']['input'];
  version: Scalars['String']['input'];
};


export type QuerySourceObjectArgs = {
  ref: SourceObjectRefInput;
};


export type QuerySourceObjectCreationMetadataArgs = {
  parent?: InputMaybe<SourceObjectRefInput>;
};


export type QuerySourceObjectsArgs = {
  kinds?: InputMaybe<Array<SourceObjectKind>>;
  parent?: InputMaybe<SourceObjectRefInput>;
};


export type QuerySourceQuerySuggestionsArgs = {
  ref?: InputMaybe<SourceObjectRefInput>;
};


export type QuerySourceRowsArgs = {
  pageOffset: Scalars['Int']['input'];
  pageSize: Scalars['Int']['input'];
  ref: SourceObjectRefInput;
  sort?: InputMaybe<Array<SortCondition>>;
  where?: InputMaybe<WhereCondition>;
};


export type QuerySourceTargetDatabasesArgs = {
  target: SourceTargetConnectionInput;
};


export type QuerySourceTargetSchemasArgs = {
  target: SourceTargetConnectionInput;
};

export type Record = {
  __typename?: 'Record';
  Key: Scalars['String']['output'];
  Value: Scalars['String']['output'];
};

export type RecordInput = {
  Extra?: InputMaybe<Array<RecordInput>>;
  Key: Scalars['String']['input'];
  Value: Scalars['String']['input'];
};

export type RowsResult = {
  __typename?: 'RowsResult';
  Columns: Array<Column>;
  DisableUpdate: Scalars['Boolean']['output'];
  Rows: Array<Array<Scalars['String']['output']>>;
  TotalCount: Scalars['Int']['output'];
};

export type SslStatus = {
  __typename?: 'SSLStatus';
  IsEnabled: Scalars['Boolean']['output'];
  Mode: Scalars['String']['output'];
};

export type SettingsConfig = {
  __typename?: 'SettingsConfig';
  DisableCredentialForm: Scalars['Boolean']['output'];
  EnableNewUI: Scalars['Boolean']['output'];
  MaxPageSize: Scalars['Int']['output'];
  MetricsEnabled?: Maybe<Scalars['Boolean']['output']>;
};

export type SettingsConfigInput = {
  MetricsEnabled?: InputMaybe<Scalars['String']['input']>;
};

export type SortCondition = {
  Column: Scalars['String']['input'];
  Direction: SortDirection;
};

export enum SortDirection {
  Asc = 'ASC',
  Desc = 'DESC'
}

export enum SourceAction {
  Browse = 'Browse',
  CreateChild = 'CreateChild',
  Delete = 'Delete',
  DeleteData = 'DeleteData',
  Execute = 'Execute',
  GenerateMockData = 'GenerateMockData',
  ImportData = 'ImportData',
  InsertData = 'InsertData',
  Inspect = 'Inspect',
  UpdateData = 'UpdateData',
  ViewContent = 'ViewContent',
  ViewDefinition = 'ViewDefinition',
  ViewGraph = 'ViewGraph',
  ViewRows = 'ViewRows'
}

export enum SourceCategory {
  Cache = 'Cache',
  Database = 'Database',
  FileStore = 'FileStore',
  ObjectStore = 'ObjectStore',
  Search = 'Search'
}

export type SourceConnectionField = {
  __typename?: 'SourceConnectionField';
  DefaultValue?: Maybe<Scalars['String']['output']>;
  Key: Scalars['String']['output'];
  Kind: SourceConnectionFieldKind;
  LabelKey: Scalars['String']['output'];
  PlaceholderKey?: Maybe<Scalars['String']['output']>;
  Required: Scalars['Boolean']['output'];
  Section: SourceConnectionFieldSection;
  SupportsOptions: Scalars['Boolean']['output'];
};

export enum SourceConnectionFieldKind {
  Boolean = 'Boolean',
  FilePath = 'FilePath',
  Password = 'Password',
  Text = 'Text'
}

export enum SourceConnectionFieldSection {
  Advanced = 'Advanced',
  Primary = 'Primary'
}

export type SourceConnectionTraits = {
  __typename?: 'SourceConnectionTraits';
  HostInputMode: SourceHostInputMode;
  HostInputURLParser: SourceHostInputUrlParser;
  SupportsCustomCAContent: Scalars['Boolean']['output'];
  Transport: SourceConnectionTransport;
};

export enum SourceConnectionTransport {
  Bridge = 'Bridge',
  File = 'File',
  Network = 'Network'
}

export type SourceContent = {
  __typename?: 'SourceContent';
  FileName: Scalars['String']['output'];
  IsBinary: Scalars['Boolean']['output'];
  MIMEType: Scalars['String']['output'];
  ModifiedAt?: Maybe<Scalars['String']['output']>;
  SizeBytes: Scalars['String']['output'];
  Text?: Maybe<Scalars['String']['output']>;
  Truncated: Scalars['Boolean']['output'];
};

export type SourceContract = {
  __typename?: 'SourceContract';
  BrowsePath: Array<SourceObjectKind>;
  DefaultObjectKind: SourceObjectKind;
  GraphScopeKind?: Maybe<SourceObjectKind>;
  Model: SourceModel;
  ObjectTypes: Array<SourceObjectType>;
  RootActions: Array<SourceAction>;
  Surfaces: Array<SourceSurface>;
};

export type SourceDiscoveryAdvancedDefault = {
  __typename?: 'SourceDiscoveryAdvancedDefault';
  Conditions: Array<SourceDiscoveryMetadataCondition>;
  DefaultValue: Scalars['String']['output'];
  Key: Scalars['String']['output'];
  MetadataKey: Scalars['String']['output'];
  ProviderTypes: Array<Scalars['String']['output']>;
  Value: Scalars['String']['output'];
};

export type SourceDiscoveryMetadataCondition = {
  __typename?: 'SourceDiscoveryMetadataCondition';
  Key: Scalars['String']['output'];
  Value: Scalars['String']['output'];
};

export type SourceDiscoveryPrefill = {
  __typename?: 'SourceDiscoveryPrefill';
  AdvancedDefaults: Array<SourceDiscoveryAdvancedDefault>;
};

/** Retains the smallest or largest primary key under ascending database key ordering. */
export enum SourceDuplicateRetentionStrategy {
  First = 'FIRST',
  Last = 'LAST'
}

export type SourceFieldConstraints = {
  __typename?: 'SourceFieldConstraints';
  AllowedValues: Array<Scalars['String']['output']>;
  CheckMax?: Maybe<Scalars['Float']['output']>;
  CheckMin?: Maybe<Scalars['Float']['output']>;
  DefaultValue?: Maybe<Scalars['String']['output']>;
  ForeignKey?: Maybe<ForeignKeyDefinition>;
  Identity: Scalars['Boolean']['output'];
  Length?: Maybe<Scalars['Int']['output']>;
  MetadataFidelity: SourceMetadataFidelity;
  Name: Scalars['String']['output'];
  Nullable?: Maybe<Scalars['Boolean']['output']>;
  Precision?: Maybe<Scalars['Int']['output']>;
  Primary: Scalars['Boolean']['output'];
  Scale?: Maybe<Scalars['Int']['output']>;
  Type: Scalars['String']['output'];
  Unique: Scalars['Boolean']['output'];
};

export enum SourceHostInputMode {
  Hostname = 'Hostname',
  HostnameOrUrl = 'HostnameOrURL',
  None = 'None'
}

export enum SourceHostInputUrlParser {
  MongoSrv = 'MongoSRV',
  None = 'None',
  Postgres = 'Postgres'
}

export type SourceLoginInput = {
  AccessToken?: InputMaybe<Scalars['String']['input']>;
  Id?: InputMaybe<Scalars['String']['input']>;
  SourceType: Scalars['String']['input'];
  Values: Array<RecordInput>;
};

export enum SourceMetadataFidelity {
  Driver = 'Driver',
  Exact = 'Exact',
  Inferred = 'Inferred',
  Sampled = 'Sampled',
  Synthetic = 'Synthetic',
  Unknown = 'Unknown',
  Unsupported = 'Unsupported'
}

export type SourceMetadataTraits = {
  __typename?: 'SourceMetadataTraits';
  Columns: SourceMetadataFidelity;
  Constraints: SourceMetadataFidelity;
  Graph: SourceMetadataFidelity;
  SystemObjectFiltering: SourceMetadataFidelity;
};

/** Read-only diagnosis. RepairAllowed still requires explicit confirmation of the reviewed plan or manual cleanup. */
export type SourceMigrationDiagnosis = {
  __typename?: 'SourceMigrationDiagnosis';
  Error?: Maybe<Scalars['String']['output']>;
  Healthy: Scalars['Boolean']['output'];
  Issues: Array<SourceMigrationIssue>;
  RepairAllowed: Scalars['Boolean']['output'];
  Token?: Maybe<Scalars['String']['output']>;
};

/** Read-only cleanup preview, or the result of an atomic cleanup confirmed against its fingerprint. */
export type SourceMigrationDuplicateCleanup = {
  __typename?: 'SourceMigrationDuplicateCleanup';
  Deleted: Scalars['Int']['output'];
  Fingerprint: Scalars['String']['output'];
  Groups: Scalars['Int']['output'];
  PrimaryKeyColumns: Array<Scalars['String']['output']>;
  RowsToDelete: Scalars['Int']['output'];
};

/** A selected source and the local migration files to diagnose or repair. */
export type SourceMigrationInput = {
  BaselineOnMigrate?: InputMaybe<Scalars['Boolean']['input']>;
  Directory: Scalars['String']['input'];
  OutOfOrder?: InputMaybe<Scalars['Boolean']['input']>;
  Target: SourceTargetConnectionInput;
  TargetSchema?: InputMaybe<Scalars['String']['input']>;
  ValidateOnMigrate?: InputMaybe<Scalars['Boolean']['input']>;
};

/** A Flyway validation issue and its locally resolved migration script. */
export type SourceMigrationIssue = {
  __typename?: 'SourceMigrationIssue';
  Code: Scalars['String']['output'];
  Message: Scalars['String']['output'];
  Recovery?: Maybe<SourceMigrationRecoveryPlan>;
  SQL: Scalars['String']['output'];
  Script: Scalars['String']['output'];
  Version: Scalars['String']['output'];
};

/** Additive DDL supported by the selected connector's recovery verifier. */
export enum SourceMigrationRecoveryKind {
  AddColumn = 'ADD_COLUMN',
  CreateIndex = 'CREATE_INDEX',
  CreateTable = 'CREATE_TABLE'
}

/** Verified schema recovery plan. No schema changes occur during diagnosis. */
export type SourceMigrationRecoveryPlan = {
  __typename?: 'SourceMigrationRecoveryPlan';
  Error?: Maybe<Scalars['String']['output']>;
  Matched: Scalars['Int']['output'];
  Pending: Scalars['Int']['output'];
  Safe: Scalars['Boolean']['output'];
  Steps: Array<SourceMigrationRecoveryStep>;
};

/** Read-only outcome for an additive schema operation. */
export enum SourceMigrationRecoveryStatus {
  Conflict = 'CONFLICT',
  Matched = 'MATCHED',
  Pending = 'PENDING'
}

/** A schema operation that will be skipped, applied, or blocked. */
export type SourceMigrationRecoveryStep = {
  __typename?: 'SourceMigrationRecoveryStep';
  /** Whether the connector can export complete records for this duplicate-data conflict. */
  DuplicateDataAvailable: Scalars['Boolean']['output'];
  Kind: SourceMigrationRecoveryKind;
  Object: Scalars['String']['output'];
  Reason: Scalars['String']['output'];
  SQL: Scalars['String']['output'];
  Status: SourceMigrationRecoveryStatus;
};

/** A single-use diagnosis plan with explicit authorization for structural recovery or completed manual cleanup. */
export type SourceMigrationRepairInput = {
  ConfirmPartialChangesResolved?: Scalars['Boolean']['input'];
  ConfirmPlanExecution?: Scalars['Boolean']['input'];
  Migration: SourceMigrationInput;
  Token: Scalars['String']['input'];
};

/** Result of repair, validation, and at most one migration retry. */
export type SourceMigrationRepairResult = {
  __typename?: 'SourceMigrationRepairResult';
  Error?: Maybe<Scalars['String']['output']>;
  Steps: Array<FlywayExecutionResult>;
  Success: Scalars['Boolean']['output'];
};

export type SourceMockDataTraits = {
  __typename?: 'SourceMockDataTraits';
  SupportsRelationalDependencies: Scalars['Boolean']['output'];
};

export enum SourceModel {
  Document = 'Document',
  Graph = 'Graph',
  KeyValue = 'KeyValue',
  Object = 'Object',
  Relational = 'Relational',
  Search = 'Search'
}

export type SourceObject = {
  __typename?: 'SourceObject';
  Actions: Array<SourceAction>;
  HasChildren: Scalars['Boolean']['output'];
  Kind: SourceObjectKind;
  Metadata: Array<Record>;
  Name: Scalars['String']['output'];
  Path: Array<Scalars['String']['output']>;
  Ref: SourceObjectRef;
};

export type SourceObjectColumns = {
  __typename?: 'SourceObjectColumns';
  Columns: Array<Column>;
  Ref: SourceObjectRef;
};

export type SourceObjectDefinitionInput = {
  Columns: Array<ColumnDefinitionInput>;
  Name: Scalars['String']['input'];
  TableOptions?: InputMaybe<Array<RecordInput>>;
};

export enum SourceObjectKind {
  Collection = 'Collection',
  Database = 'Database',
  Function = 'Function',
  Index = 'Index',
  Item = 'Item',
  Key = 'Key',
  Procedure = 'Procedure',
  Schema = 'Schema',
  Sequence = 'Sequence',
  Table = 'Table',
  Trigger = 'Trigger',
  View = 'View'
}

export type SourceObjectRef = {
  __typename?: 'SourceObjectRef';
  Kind: SourceObjectKind;
  Locator: Scalars['String']['output'];
  Path: Array<Scalars['String']['output']>;
};

export type SourceObjectRefInput = {
  Kind: SourceObjectKind;
  Locator?: InputMaybe<Scalars['String']['input']>;
  Path: Array<Scalars['String']['input']>;
};

export type SourceObjectType = {
  __typename?: 'SourceObjectType';
  Actions: Array<SourceAction>;
  DataShape: DataShape;
  Kind: SourceObjectKind;
  PluralLabel: Scalars['String']['output'];
  SingularLabel: Scalars['String']['output'];
  Views: Array<SourceView>;
};

export type SourcePresentationTraits = {
  __typename?: 'SourcePresentationTraits';
  ProfileLabelStrategy: SourceProfileLabelStrategy;
  SchemaFidelity: SourceSchemaFidelity;
};

export type SourceProfile = {
  __typename?: 'SourceProfile';
  DisplayName: Scalars['String']['output'];
  Id: Scalars['String']['output'];
  IsEnvironmentDefined: Scalars['Boolean']['output'];
  /** Stable identifier of the project owning this connection, when project-scoped. */
  ProjectId?: Maybe<Scalars['String']['output']>;
  /** Display name of the project owning this connection. */
  ProjectName?: Maybe<Scalars['String']['output']>;
  SSLConfigured: Scalars['Boolean']['output'];
  Source: Scalars['String']['output'];
  SourceType: Scalars['String']['output'];
  Values: Array<Record>;
};

export enum SourceProfileLabelStrategy {
  Database = 'Database',
  Default = 'Default',
  Hostname = 'Hostname'
}

export type SourceProfileLoginInput = {
  Id: Scalars['String']['input'];
  Values?: InputMaybe<Array<RecordInput>>;
};

export enum SourceQueryExplainMode {
  Explain = 'Explain',
  ExplainAnalyze = 'ExplainAnalyze',
  ExplainPipeline = 'ExplainPipeline',
  None = 'None'
}

export type SourceQuerySuggestion = {
  __typename?: 'SourceQuerySuggestion';
  category: Scalars['String']['output'];
  description: Scalars['String']['output'];
};

export type SourceQueryTraits = {
  __typename?: 'SourceQueryTraits';
  ExplainMode: SourceQueryExplainMode;
  SequentialPaginationOnly: Scalars['Boolean']['output'];
  SupportsAnalyze: Scalars['Boolean']['output'];
  SupportsMultiStatement: Scalars['Boolean']['output'];
  SupportsScripts: Scalars['Boolean']['output'];
  SupportsSqlImport: Scalars['Boolean']['output'];
  SupportsStreaming: Scalars['Boolean']['output'];
};

export type SourceSslMode = {
  __typename?: 'SourceSSLMode';
  aliases: Array<Scalars['String']['output']>;
  value: Scalars['String']['output'];
};

export enum SourceSchemaFidelity {
  Exact = 'Exact',
  Sampled = 'Sampled'
}

export type SourceSession = {
  __typename?: 'SourceSession';
  Database: Scalars['String']['output'];
  Id?: Maybe<Scalars['String']['output']>;
  SourceType: Scalars['String']['output'];
};

export type SourceSessionMetadata = {
  __typename?: 'SourceSessionMetadata';
  AliasMap: Array<Record>;
  Operators: Array<Scalars['String']['output']>;
  QueryLanguages: Array<Scalars['String']['output']>;
  SourceType: Scalars['String']['output'];
  TypeDefinitions: Array<TypeDefinition>;
};

export enum SourceSurface {
  Browser = 'Browser',
  Chat = 'Chat',
  Graph = 'Graph',
  Query = 'Query'
}

export type SourceTargetConnectionInput = {
  AccessToken?: InputMaybe<Scalars['String']['input']>;
  Database?: InputMaybe<Scalars['String']['input']>;
  ProfileId?: InputMaybe<Scalars['String']['input']>;
  SourceType?: InputMaybe<Scalars['String']['input']>;
  Values?: InputMaybe<Array<RecordInput>>;
};

export type SourceTraits = {
  __typename?: 'SourceTraits';
  Connection: SourceConnectionTraits;
  Metadata: SourceMetadataTraits;
  MockData: SourceMockDataTraits;
  Presentation: SourcePresentationTraits;
  Query: SourceQueryTraits;
};

export type SourceType = {
  __typename?: 'SourceType';
  Category: SourceCategory;
  ConnectionFields: Array<SourceConnectionField>;
  Connector: Scalars['String']['output'];
  Contract: SourceContract;
  DiscoveryPrefill: SourceDiscoveryPrefill;
  Id: Scalars['String']['output'];
  IsAWSManaged: Scalars['Boolean']['output'];
  Label: Scalars['String']['output'];
  SSLModes: Array<SourceSslMode>;
  Traits: SourceTraits;
};

export enum SourceView {
  Binary = 'Binary',
  Graph = 'Graph',
  Grid = 'Grid',
  Json = 'JSON',
  Metadata = 'Metadata',
  Sql = 'SQL',
  Text = 'Text'
}

export type StatusResponse = {
  __typename?: 'StatusResponse';
  Status: Scalars['Boolean']['output'];
};

export type StorageUnitColumns = {
  __typename?: 'StorageUnitColumns';
  Columns: Array<Column>;
  StorageUnit: Scalars['String']['output'];
};

export type TableCreationCapabilities = {
  __typename?: 'TableCreationCapabilities';
  ClusteringKey: Scalars['Boolean']['output'];
  KeyValueType: Scalars['Boolean']['output'];
  OrderKey: Scalars['Boolean']['output'];
  PartitionKey: Scalars['Boolean']['output'];
  RequiresPrimaryKey: Scalars['Boolean']['output'];
};

export enum TypeCategory {
  Binary = 'binary',
  Boolean = 'boolean',
  Datetime = 'datetime',
  Json = 'json',
  Numeric = 'numeric',
  Other = 'other',
  Text = 'text'
}

export type TypeDefinition = {
  __typename?: 'TypeDefinition';
  category: TypeCategory;
  defaultLength?: Maybe<Scalars['Int']['output']>;
  defaultPrecision?: Maybe<Scalars['Int']['output']>;
  hasLength: Scalars['Boolean']['output'];
  hasPrecision: Scalars['Boolean']['output'];
  id: Scalars['String']['output'];
  insertFunc?: Maybe<Scalars['String']['output']>;
  label: Scalars['String']['output'];
  tableModel?: Maybe<Scalars['String']['output']>;
};

export type UpdateInfo = {
  __typename?: 'UpdateInfo';
  currentVersion: Scalars['String']['output'];
  latestVersion: Scalars['String']['output'];
  releaseURL: Scalars['String']['output'];
  updateAvailable: Scalars['Boolean']['output'];
};

export type WhereCondition = {
  And?: InputMaybe<OperationWhereCondition>;
  Atomic?: InputMaybe<AtomicWhereCondition>;
  Or?: InputMaybe<OperationWhereCondition>;
  Type: WhereConditionType;
};

export enum WhereConditionType {
  And = 'And',
  Atomic = 'Atomic',
  Or = 'Or'
}
